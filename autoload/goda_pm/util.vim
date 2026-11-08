const s:is_windows = has('win32') || has('win64')

function! s:is_headless_mode() abort
  return (has('nvim') && nvim_list_uis()->empty())
          \ || (!has('nvim') && mode(v:true) ==# 'ce')
endfunction

function goda_pm#util#_error(string, name = 'goda-pm') abort
  const detect_shell = s:is_headless_mode()

  echohl Error
  for line in
        \ (a:string->type() ==# v:t_string ? a:string : a:string->string())
        \ ->split("\n")->filter({ _, val -> val != ''})
    echomsg printf('[%s] %s', a:name, line)

    if s:is_headless_mode()
      " When it is executed from shell, new line is not added in ":echomsg".
      echo "\n"
    endif
  endfor
  echohl None

  if !has('nvim') && detect_shell
    " When it is executed from shell, new line is not added in ":echomsg".
    echo ""
  endif
endfunction

" A message, as goda_pm#util#_error() (the exts call it: Vim has no function
" for :echomsg).
function goda_pm#util#_print_message(msg, name = 'goda-pm') abort
  const detect_shell = s:is_headless_mode()

  for mes in a:msg->type() ==# v:t_list ? a:msg : a:msg->split('\n')
    echomsg printf('[%s] %s', a:name, mes)

    if has('nvim') && detect_shell
      " When it is executed from shell, new line is not added in ":echomsg".
      echo "\n"
    endif
  endfor

  if !has('nvim') && detect_shell
    " When it is executed from shell, new line is not added in ":echomsg".
    echo ""
  endif
endfunction

function goda_pm#util#_get_plugins(plugins=[]) abort
  return a:plugins->empty() ?
        \ g:goda_pm.state.plugins->values() :
        \ a:plugins->goda_pm#util#_convert2list()
        \ ->map({ _, val -> val->type() == v:t_dict ? val : val->goda_pm#get() })
        \ ->filter({ _, val -> !val->empty() })
        \ ->sort({ a, b -> b->get('priority', 0) - a->get('priority', 0) })
endfunction
function goda_pm#util#_get_lazy_plugins() abort
  return g:goda_pm.state.plugins->values()
        \ ->filter({ _, val -> !val.sourced && val.rtp !=# '' })
        \ ->sort({ a, b -> b->get('priority', 0) - a->get('priority', 0) })
endfunction

function goda_pm#util#_get_runtime_path() abort
  return goda_pm#util#_substitute_path($VIMRUNTIME)
endfunction
function goda_pm#util#_get_base_path() abort
  return 'g:goda_pm'->exists() ? g:goda_pm.settings->get('base_path', '') : ''
endfunction
function goda_pm#util#_get_name() abort
  return 'g:goda_pm'->exists()
        \ ? g:goda_pm.settings->get('name', v:progname->fnamemodify(':r'))
        \ : v:progname->fnamemodify(':r')
endfunction
function goda_pm#util#_check_files(base_path, name) abort
  const state_path = printf('%s/%s/state.vim', a:base_path, a:name)
  const state_mtime = state_path->getftime()

  if state_mtime < 0
    return g:goda_pm.state.check_files->copy()
  endif

  const missing_or_newer = g:goda_pm.state.check_files->copy()
        \ ->filter({ _, val ->
        \   val->goda_pm#util#_expand()->getftime() < 0
        \   || state_mtime < val->goda_pm#util#_expand()->getftime()
        \ })

  return missing_or_newer
endfunction

function goda_pm#util#_convert2list(expr) abort
  return
        \   a:expr->type() ==# v:t_list
        \ ? a:expr->copy()
        \ : a:expr->type() ==# v:t_string
        \ ? (a:expr ==# '' ? [] : a:expr->split('\r\?\n', 1))
        \ : [a:expr]
endfunction

function goda_pm#util#_split_rtp(runtimepath) abort
  if a:runtimepath->stridx('\,') < 0
    let rtps = a:runtimepath->split(',')
  else
    let rtps = a:runtimepath->split('\\\@<!\%(\\\\\)*\zs,')
          \ ->map({ _, val -> val->substitute('\\\([\\,]\)', '\1', 'g') })
  endif

  return rtps->map({ _, val ->
        \   val->goda_pm#util#_substitute_path()
        \ })->filter({ _, val -> val->goda_pm#util#_expand()->isdirectory() })
endfunction
function goda_pm#util#_join_rtp(list, runtimepath, current_rtp) abort
  let list = goda_pm#util#_uniq(a:list)
  const needs_escape =
        \ a:runtimepath->stridx('\,') >= 0 || a:current_rtp->stridx(',') >= 0
  return needs_escape
        \ ? list->map({ _, val -> s:escape(val) })->join(',')
        \ : list->join(',')
endfunction
function! s:escape(path) abort
  " Escape a path for runtimepath.
  return a:path->substitute(',\|\\,\@=', '\\\0', 'g')
endfunction

function goda_pm#util#_add_after(rtps, path) abort
  const idx = a:rtps->index($VIMRUNTIME->goda_pm#util#_substitute_path())
  call insert(a:rtps, a:path, (idx <= 0 ? -1 : idx + 1))
endfunction

function goda_pm#util#_expand(path) abort
  if a:path is v:null
    return ''
  endif

  let path = (a:path =~# '^\$\h\w*') ? a:path->substitute(
        \ '^\$\h\w*', '\=eval(submatch(0))', '') : a:path
  if path =~# '^\~'
    let path = path->fnamemodify(':p')
  endif
  return ((s:is_windows && path =~# '\\') ?
        \ path->goda_pm#util#_substitute_path() : path)->substitute('/$', '', '')
endfunction
function goda_pm#util#_substitute_path(path) abort
  return ((s:is_windows || has('win32unix')) && a:path =~# '\\') ?
        \ a:path->tr('\', '/') : a:path
endfunction

function goda_pm#util#_call_hook(hook_name, plugins = [], args = {}) abort
  const hook = 'hook_' .. a:hook_name
  let plugins = a:plugins->goda_pm#util#_get_plugins()->goda_pm#util#_tsort()
        \ ->filter({ _, val ->
        \    ((a:hook_name !=# 'source'
        \      && a:hook_name !=# 'post_source') || val.sourced)
        \    && val->has_key(hook) && val.path->isdirectory()
        \    && (!val->has_key('if') || val.if->eval())
        \ })
  for plugin in plugins
    call goda_pm#util#_execute_hook(
          \ plugin, hook, plugin.name, plugin[hook], a:args)
  endfor

  if a:hook_name ==# 'source' || a:hook_name ==# 'post_source'
    " Check multiple_hooks
    for hooks in g:goda_pm.state.multiple_hooks
      if hooks->get(hook, '') ==# ''
        continue
      endif

      if hooks.plugins->len() ==#
            \ goda_pm#util#_get_plugins(hooks.plugins)
            \ ->filter({ _, val -> val.sourced })->len()
        " All plugins are sourced

        call goda_pm#util#_execute_hook(
              \ {}, hook, string(hooks.plugins), hooks[hook], a:args)

        " Skip twice call
        let hooks[hook] = ''
      endif
    endfor
  endif
endfunction
function goda_pm#util#_execute_hook(
      \ plugin, hook_name, plugin_name, hook, args = {}) abort
  " Skip twice call
  if !a:plugin->empty()
    if !a:plugin->has_key('called')
      let a:plugin.called = {}
    endif
    if a:plugin.called->has_key(a:hook->string())
      return
    endif
  endif

  let g:goda_pm#hook_result = v:null
  let g:goda_pm#hook_args = a:args

  try
    " NOTE: hook may contain \r in Windows
    const cmds = a:hook->split('\r\?\n')
          \ ->map({ _, val -> val->substitute('\r', '', 'g')})
    if !cmds->empty() && cmds[0] =~# '^\s*vim9script' && exists(':vim9')
      vim9 call execute(cmds[1 : ], '')
    else
      call execute(cmds, '')
    endif
  catch
    call goda_pm#util#_error(
          \ printf('Error occurred while executing %s: %s',
          \        a:hook_name,
          \        a:plugin_name))
    call goda_pm#util#_error('<stack>'->expand())
    call goda_pm#util#_error(v:exception)
  endtry

  let g:goda_pm#hook_args = v:null

  if !a:plugin->empty()
    let a:plugin.called[string(a:hook)] = v:true
  endif
endfunction

function goda_pm#util#_tsort(plugins) abort
  let sorted = []
  let mark = {}
  for target in a:plugins
    call s:tsort_impl(target, mark, sorted)
  endfor

  return sorted
endfunction

function s:tsort_impl(target, mark, sorted) abort
  if a:target->empty() || a:mark->has_key(a:target.name)
    return
  endif

  let a:mark[a:target.name] = v:true
  if a:target->has_key('depends')
    for depend in a:target.depends
      call s:tsort_impl(depend->goda_pm#get(), a:mark, a:sorted)
    endfor
  endif

  call add(a:sorted, a:target)
endfunction

function goda_pm#util#_clear_state(name) abort
  const startup = printf(
        \ '%s/%s/startup.vim',
        \ g:goda_pm.settings.base_path, a:name)
  if startup->filereadable()
    call delete(startup)
  endif
  const state = printf('%s/%s/state.vim', g:goda_pm.settings.base_path, a:name)
  if state->filereadable()
    call delete(state)
  endif
endfunction

function goda_pm#util#_get_normalized_name(plugin) abort
  return a:plugin.name
        \ ->fnamemodify(':r')
        \ ->substitute(
        \   '\c^\%(n\?vim\|dps\|denops\)[_-]\|[_-]n\?vim$', '', 'g')
endfunction

function goda_pm#util#_generate_ftplugin(runtimepath, ftplugin) abort
  let generated = {}

  " Merge
  let ftplugin = {}
  for [key, string] in a:ftplugin->items()
    for ft in (key ==# '_' ? ['_'] : key->split('_'))
      if !ftplugin->has_key(ft)
        if ft ==# '_'
          let ftplugin[ft] = []
        else
          let ftplugin[ft] =<< trim END
            if 'b:undo_ftplugin'->exists()
              let b:undo_ftplugin ..= '|'
            else
              let b:undo_ftplugin = ''
            endif
          END
        endif
      endif
      let ftplugin[ft] += string->split('\n')
    endfor
  endfor

  " Generate ftplugin.vim
  let ftplugin_generated = s:get_default_ftplugin()
  let ftplugin_generated += ['function! s:after_ftplugin()']
  let ftplugin_generated += ftplugin->get('_', [])
  let ftplugin_generated += ['endfunction']
  let generated[a:runtimepath .. '/ftplugin.vim'] = ftplugin_generated

  " Generate after/ftplugin
  const after = a:runtimepath .. '/after/ftplugin'
  for [filetype, list] in ftplugin->items()
        \ ->filter({ _, val -> val[0] !=# '_' })
    let generated[printf('%s/%s.vim', after, filetype)] = list
  endfor

  return generated
endfunction
function s:get_default_ftplugin() abort
  let default_ftplugin =<< trim END
    if exists('g:did_load_ftplugin')
      finish
    endif
    let g:did_load_ftplugin = 1

    augroup filetypeplugin
      autocmd!
      autocmd FileType * call s:ftplugin()
    augroup END

    function! s:ftplugin()
      if 'b:undo_ftplugin'->exists()
        silent! execute b:undo_ftplugin
        unlet! b:undo_ftplugin b:did_ftplugin
      endif

      let filetype = '<amatch>'->expand()
      if filetype !=# ''
        if &cpoptions =~# 'S' && 'b:did_ftplugin'->exists()
          unlet b:did_ftplugin
        endif
        for ft in filetype->split('\.')
          execute 'runtime!'
          \ 'ftplugin/' .. ft .. '.vim'
          \ 'ftplugin/' .. ft .. '_*.vim'
          \ 'ftplugin/' .. ft .. '/*.vim'
          if has('nvim')
            silent! execute 'runtime!'
            \ 'ftplugin/' .. ft .. '.lua'
            \ 'ftplugin/' .. ft .. '_*.lua'
            \ 'ftplugin/' .. ft .. '/*.lua'
          endif
        endfor
      endif
      call s:after_ftplugin()
    endfunction

  END
  return default_ftplugin
endfunction

function goda_pm#util#_dos2unix(path) abort
  call writefile(
        \   readfile(a:path)
        \     ->map({ _, val -> val->substitute('\r', '', 'g')}),
        \   a:path
        \ )
endfunction

function goda_pm#util#_check_clean() abort
  const plugins_directories = goda_pm#get()->values()
        \ ->map({ _, val -> goda_pm#util#_substitute_path(val.path) })
  const path = goda_pm#util#_substitute_path(
        \ 'repos/*/*/*'->globpath(g:goda_pm.settings.base_path, v:true))
  return path->split("\n")->filter( { _, val ->
        \  val->isdirectory() && val->fnamemodify(':t') !=# 'goda-pm.vim'
        \  && plugins_directories->index(val) < 0
        \ })
endfunction

function goda_pm#util#_uniq(list) abort
  let list = a:list->copy()
  let i = 0
  let seen = {}
  while i < list->len()
    let key = list[i]
    if key !=# '' && seen->has_key(key)
      call remove(list, i)
    else
      if key !=# ''
        let seen[key] = v:true
      endif
      let i += 1
    endif
  endwhile
  return list
endfunction

function goda_pm#util#_helptags(dir) abort
  execute 'silent helptags' a:dir->fnameescape()
endfunction
