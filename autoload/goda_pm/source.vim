const s:sep = has('win32') ? '\' : '/'
function goda_pm#source#_source(plugins, function_prefix) abort
  let plugins = a:plugins->goda_pm#util#_convert2list()
  if plugins->empty()
    return []
  endif

  if plugins[0]->type() != v:t_dict
    let plugins = a:plugins->goda_pm#util#_convert2list()
          \ ->map({ _, val -> g:goda_pm.state.plugins->get(val, {}) })
  endif

  call filter(plugins, { _, val ->
        \  !val->empty() && !val.sourced && val.rtp !=# ''
        \ })
  if plugins->empty()
    " Nothing to source (an event of plugins already sourced, say): the
    " runtimepath and the filetype plugins stay as they are.
    call s:reload_autoload(a:function_prefix)
    return []
  endif

  let rtps = &runtimepath->goda_pm#util#_split_rtp()
  const index = rtps->index(goda_pm#util#_get_runtime_path())
  if index < 0
    return []
  endif

  let sourced = []
  for plugin in plugins
    call s:source_plugin(rtps, index, plugin, sourced)
  endfor
  if sourced->empty()
    " (Their "if" or "external_commands" kept them out.)
    call s:reload_autoload(a:function_prefix)
    return []
  endif

  const filetype_before = 'autocmd FileType'->execute()

  let &runtimepath = rtps->goda_pm#util#_join_rtp(&runtimepath, '')

  call goda_pm#util#_call_hook('source', sourced)

  call s:reload_autoload(a:function_prefix)

  " Reload script files.
  for plugin in sourced
    for directory in
          \ ['ftdetect', 'after/ftdetect', 'plugin', 'after/plugin']
          \ ->filter({ _, val -> (plugin.rtp .. '/' .. val)->isdirectory() })
          \ ->map({ _, val -> plugin.rtp .. '/' .. val })
      if directory =~# 'ftdetect'
        if !plugin->get('merge_ftdetect')
          execute 'augroup filetypedetect'
        endif
      endif

      let files = []
      let files += (directory .. '/**/*.vim')->glob(v:true, v:true)
      if has('nvim')
        let files += (directory .. '/**/*.lua')->glob(v:true, v:true)
      endif

      for file in files
        execute 'source' file->fnameescape()
      endfor

      if directory =~# 'ftdetect'
        execute 'augroup END'
      endif
    endfor

    if !has('vim_starting')
      let augroup = plugin->get('augroup',
            \ plugin->goda_pm#util#_get_normalized_name())
      let events = [
            \   'VimEnter', 'BufRead', 'BufEnter',
            \   'BufWinEnter', 'WinEnter',
            \ ]
      if has('gui_running') && &term ==# 'builtin_gui'
        call add(events, 'GUIEnter')
      endif
      for event in events
        if ('#' .. augroup .. '#' .. event)->exists()
          silent execute 'doautocmd' augroup event
        endif
      endfor

      " Register for lazy loaded denops plugin
      if (plugin.rtp .. '/denops')->isdirectory()
            \ && 'g:loaded_denops'->exists()
        for name in 'denops/*/main.ts'
              \ ->globpath(plugin.rtp, v:true, v:true)
              \ ->map({ _, val -> val->fnamemodify(':h:t')})
              \ ->filter({ _, val -> !val->denops#plugin#is_loaded() })

          if denops#server#status() ==# 'running'
            call denops#plugin#load(
                  \   name,
                  \   [plugin.rtp, 'denops', name, 'main.ts']->join(s:sep),
                  \ )

            call denops#plugin#wait(name)
            redraw
          endif
        endfor
      endif
    endif
  endfor

  const filetype_after = 'autocmd FileType'->execute()

  const is_reset = s:is_reset_ftplugin(sourced)
  if is_reset
    " NOTE: filetype plugins must be reset to load new ftplugins
    call s:reset_ftplugin()
  endif

  if (is_reset || filetype_before !=# filetype_after) && &l:filetype !=# ''
    " Recall FileType autocmd
    " NOTE: ftplugins may be broken.
    silent! let &l:filetype = &l:filetype
  endif

  if !has('vim_starting')
    call goda_pm#util#_call_hook('post_source', sourced)
  endif

  return sourced
endfunction

" The autoload script of {function_prefix} ("foo#bar#"), sourced again: an
" autoload script is not loaded twice.
function s:reload_autoload(function_prefix) abort
  if a:function_prefix->stridx('#') > 0
    execute 'runtime' 'autoload/' .. a:function_prefix
          \ ->substitute('#$', '.vim', '')->substitute('#', '/', 'g')
  endif
endfunction

function s:source_plugin(rtps, index, plugin, sourced) abort
  if a:plugin.sourced
        \ || a:sourced->index(a:plugin) >= 0
        \ || (a:plugin->has_key('if') && !a:plugin.if->eval())
        \ || !a:plugin.path->isdirectory()
    return
  endif

  " External commands check
  for cmd in a:plugin->get('external_commands', [])
        \ ->goda_pm#util#_convert2list()
        \ ->filter({ _, val ->
        \   !val->executable()
        \ })
    call goda_pm#util#_error($'{a:plugin.name} requires "{cmd}".')
    return
  endfor

  call insert(a:sourced, a:plugin)

  let index = a:index

  " NOTE: on_source must be sourced before depends
  for source in goda_pm#util#_get_lazy_plugins()
        \ ->filter({ _, val ->
        \   val->get('on_source', [])
        \   ->goda_pm#util#_convert2list()
        \   ->index(a:plugin.name) >= 0
        \ })
    if s:source_plugin(a:rtps, index, source, a:sourced)
      let index += 1
    endif
  endfor

  " Load dependencies
  for name in a:plugin->get('depends', [])
        \ ->goda_pm#util#_convert2list()
    if !g:goda_pm.state.plugins->has_key(name)
      call goda_pm#util#_error(printf(
            \ 'Plugin "%s" depends "%s" but it is not found.',
            \ a:plugin.name, name))
      continue
    endif

    if !a:plugin.lazy && g:goda_pm.state.plugins[name].lazy
      call goda_pm#util#_error(printf(
            \ 'Not lazy plugin "%s" depends lazy "%s" plugin.',
            \ a:plugin.name, name))
      continue
    endif

    if s:source_plugin(a:rtps, index, g:goda_pm.state.plugins[name], a:sourced)
      let index += 1
    endif
  endfor

  let a:plugin.sourced = v:true

  if a:plugin->has_key('dummy_commands')
    for command in a:plugin.dummy_commands
      " NOTE: exists() check does not work well
      silent! execute 'delcommand' command
    endfor
    let a:plugin.dummy_commands = []
  endif

  if a:plugin->has_key('dummy_mappings')
    for [mode, map] in a:plugin.dummy_mappings
      if map->hasmapto(mode)
        " NOTE: unmap may be failed
        silent! execute mode..'unmap' map
      endif
    endfor
    let a:plugin.dummy_mappings = []
  endif

  if !a:plugin.merged || a:plugin->get('local', 0)
    call insert(a:rtps, a:plugin.rtp, index)
    if (a:plugin.rtp .. '/after')->isdirectory()
      call goda_pm#util#_add_after(a:rtps, a:plugin.rtp .. '/after')
    endif
  endif

  " Load on_post_source
  for source in goda_pm#util#_get_lazy_plugins()
        \ ->filter({ _, val ->
        \   val->get('on_post_source', [])
        \   ->goda_pm#util#_convert2list()
        \   ->index(a:plugin.name) >= 0
        \ })
    if s:source_plugin(a:rtps, index, source, a:sourced)
      let index += 1
    endif
  endfor
endfunction

function s:is_reset_ftplugin(plugins) abort
  if &l:filetype ==# ''
    return 0
  endif

  const ft = &l:filetype
  for plugin in a:plugins
    " Only the directories the plugin has: most have none of them.
    for dir in ['ftplugin', 'indent', 'after/ftplugin', 'after/indent']
          \ ->map({ _, val -> plugin.rtp .. '/' .. val })
          \ ->filter({ _, val -> val->isdirectory() })
      " (let: a const of the loop is E995 the second time.)
      let base = dir .. '/' .. ft
      if (base .. '.vim')->filereadable() || (base .. '.lua')->filereadable()
        return 1
      endif
      if dir =~# 'ftplugin$' && (base->isdirectory()
            \ || (base .. '_*.vim')->glob(v:true) !=# ''
            \ || (base .. '_*.lua')->glob(v:true) !=# '')
        return 1
      endif
    endfor
  endfor

  return 0
endfunction

function s:reset_ftplugin() abort
  const filetype_state = 'filetype'->execute()

  if 'b:did_indent'->exists() || 'b:did_ftplugin'->exists()
    filetype plugin indent off
  endif

  if filetype_state =~# 'plugin:ON'
    silent! filetype plugin on
  endif

  if filetype_state =~# 'indent:ON'
    silent! filetype indent on
  endif
endfunction
