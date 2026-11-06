" goda-pm.vim - a plugin manager for Vim/Neovim on goda.vim, the successor
" of dpp.vim.
"
" The startup needs none of the binary: goda_pm#min#load_state() sources the
" state. The binary (the user's own build of a configuration, see the
" README) makes the state (goda_pm#make_state()) and has the exts
" (goda_pm#sync_ext_action()). Only those start it, so goda.vim is needed
" only by them.

" The version of this Vim script that the binary goes with (ScriptVersion of
" its Go side): goda.vim refuses a binary of another one, and says to build
" it again. Bumped with it when they no longer work together.
let s:version = #{version: 1, module: 'github.com/Shougo/goda-pm.vim'}

" The calls that are waited for (goda_pm#_done()), by id.
let s:results = {}
" The calls the binary runs, by id: goda_pm#build() waits for them.
let s:running = {}
let s:build_waits = 0
let s:next_id = 0

" ---------------------------------------------------------------------------
" Setup

" The binary is machine specific, so it is not built into the configuration
" directory (which belongs in your dotfiles) but into a cache directory.
function! s:cache_dir() abort
  if has('nvim')
    let base = stdpath('cache')
  elseif !empty($XDG_CACHE_HOME)
    let base = $XDG_CACHE_HOME . '/vim'
  elseif has('win32') && !empty($LOCALAPPDATA)
    let base = $LOCALAPPDATA . '/vim'
  else
    let base = expand('~/.cache/vim')
  endif
  return base . '/goda-pm'
endfunction

function! s:default_bin() abort
  if exists('g:goda_pm#binary')
    return expand(g:goda_pm#binary)
  endif
  return s:cache_dir() . '/goda-pm' . (has('win32') ? '.exe' : '')
endfunction

function! s:config_dir() abort
  if exists('g:goda_pm#config_dir')
    return expand(g:goda_pm#config_dir)
  endif
  return (has('nvim') ? stdpath('config') : expand('~/.vim')) . '/goda-pm'
endfunction

" Build the user's configuration (a Go module in g:goda_pm#config_dir) with
" goda#build(): with the plugins installed (g:goda#workspace). The running
" binary is stopped, and the new one starts with the next request. When the
" build fails, the previous binary keeps running.
"
" While the binary runs an action or makes the state (a hook_post_update of
" the installer calls it, say), the build waits for them: it would stop the
" binary in the middle of them.
function! goda_pm#build() abort
  if !empty(s:running) && goda#is_running('pm')
    let s:build_waits = 1
    echomsg 'goda-pm: the build waits for the running actions'
    return
  endif
  let s:running = {}
  call goda#build(#{name: 'pm', dir: s:config_dir(), bin: s:default_bin()})
  call goda#register('pm', function('s:start'), s:version)
endfunction

function! s:build_waited(...) abort
  try
    call goda_pm#build()
  catch
    call goda_pm#util#_error(v:exception)
  endtry
endfunction

" Start the binary: goda.vim calls it when a request needs the plugin "pm"
" and no binary serves it.
function! s:start() abort
  let bin = s:default_bin()
  if !executable(bin)
    throw 'goda-pm: ' . bin . ' does not exist yet; build it with: call goda_pm#build()'
  endif
  let plugins = goda#start(bin)
  if index(plugins, 'pm') < 0
    if !empty(plugins)
      call goda#stop(plugins[0])
    endif
    throw 'goda-pm: ' . bin . ' does not contain the "pm" plugin'
  endif
  " A binary built from an invalid configuration starts anyway and says why.
  let status = goda#request('pm', 'status')
  if type(status) == v:t_string && status !=# ''
    call goda#stop('pm')
    throw 'goda-pm: fix the configuration in ' . s:config_dir()
          \ . ' and run goda_pm#build(): ' . status
  endif
endfunction

" Register the binary with goda.vim: not when this script is loaded, for
" goda_pm#source() and the lazy loading do not need goda.vim.
function! s:register() abort
  if !exists('s:registered')
    call goda#register('pm', function('s:start'), s:version)
    let s:registered = v:true
  endif
endfunction

" Is the configuration newer than the binary (or is there no binary)? Not
" with g:goda_pm#binary: that one is built by the user.
function! s:needs_build() abort
  if exists('g:goda_pm#binary') || !isdirectory(s:config_dir())
    return v:false
  endif
  let bin_time = getftime(s:default_bin())
  if bin_time < 0
    return v:true
  endif
  for file in globpath(s:config_dir(), '*.go', v:true, v:true)
        \ + globpath(s:config_dir(), 'go.mod', v:true, v:true)
    if getftime(file) > bin_time
      return v:true
    endif
  endfor
  return v:false
endfunction

function! s:prepare() abort
  if !exists('g:goda_pm') || !has_key(g:goda_pm, '_state_version')
    call goda_pm#min#_init()
  endif
  if g:goda_pm._is_sudo
    throw 'goda-pm: it is disabled in sudo'
  endif
  if s:needs_build()
    call goda_pm#build()
  endif
  call s:register()
endfunction

" Call the method of the binary in the background: it gives back with
" goda_pm#_done() the id this returns.
function! s:call(method, args) abort
  let s:next_id += 1
  let id = s:next_id
  let s:running[id] = 1
  try
    call call('goda#notify', ['pm', a:method] + a:args + [id])
  catch
    call remove(s:running, id)
    throw v:exception
  endtry
  return id
endfunction

" Wait for the call id, and give its result.
function! s:wait(id) abort
  let s:results[a:id] = v:null
  while s:results[a:id] is v:null
    sleep 10m
  endwhile
  let [result, error] = remove(s:results, a:id)
  if error !=# ''
    throw error
  endif
  return result
endfunction

" The binary tells the call id is done.
function! goda_pm#_done(id, result, error) abort
  if has_key(s:running, a:id)
    call remove(s:running, a:id)
  endif
  if s:build_waits && empty(s:running)
    " Not in the call of the binary: the build stops it.
    let s:build_waits = 0
    call timer_start(0, function('s:build_waited'))
  endif
  if has_key(s:results, a:id)
    let s:results[a:id] = [a:result, a:error]
  elseif a:error !=# ''
    call goda_pm#util#_error(a:error)
  endif
endfunction

" ---------------------------------------------------------------------------
" API (that of dpp.vim)

function goda_pm#get(name = '') abort
  return !'g:goda_pm'->exists() || !g:goda_pm->has_key('state')
        \ ? {}
        \ : a:name ==# ''
        \ ? g:goda_pm.state.plugins->copy()
        \ : g:goda_pm.state.plugins->get(a:name, {})
endfunction

function goda_pm#source(plugins = [], function_prefix = '') abort
  const plugins_val = a:plugins->empty() && 'g:goda_pm'->exists()
        \ ? g:goda_pm.state.plugins->values()
        \ : a:plugins
  return goda_pm#source#_source(plugins_val, a:function_prefix)
endfunction

function! s:check_initialized() abort
  if !'g:goda_pm'->exists()
        \ || !g:goda_pm.settings->has_key('base_path')
    call goda_pm#util#_error('goda-pm.vim is not initialized yet.')
    call goda_pm#util#_error(
          \ 'Please check goda_pm#min#load_state() is called.')
    return 1
  endif
endfunction

" Run the action of the ext, and give its result.
function goda_pm#sync_ext_action(ext_name, action_name, action_params = {}) abort
  if s:check_initialized()
    return
  endif
  try
    call s:prepare()
    return s:wait(s:call('extAction',
          \ [a:ext_name, a:action_name, a:action_params]))
  catch
    call goda_pm#util#_error(v:exception)
  endtry
endfunction

" Run the action of the ext in the background.
function goda_pm#async_ext_action(ext_name, action_name, action_params = {}) abort
  if s:check_initialized()
    return
  endif
  try
    call s:prepare()
    call s:call('extAction', [a:ext_name, a:action_name, a:action_params])
  catch
    call goda_pm#util#_error(v:exception)
  endtry
endfunction

" Make the state in the background ("User GodaPm:makeStatePost" when it is
" done). It builds the configuration first when it is newer than the
" binary.
function goda_pm#make_state(base_path = '', name = '', extra_args = {}) abort
  return s:make_state(a:base_path, a:name, a:extra_args, v:false)
endfunction

" Make the state, and wait for it.
function goda_pm#make_state_sync(base_path = '', name = '', extra_args = {}) abort
  return s:make_state(a:base_path, a:name, a:extra_args, v:true)
endfunction

function! s:make_state(base_path, name, extra_args, sync) abort
  if !'g:goda_pm'->exists() || !has_key(g:goda_pm, '_state_version')
    call goda_pm#min#_init()
  endif
  const base_path = (a:base_path ==# ''
        \ ? g:goda_pm.settings->get('base_path', '') : a:base_path)
        \ ->goda_pm#util#_expand()
  const name = a:name ==# '' ? goda_pm#util#_get_name() : a:name
  const extra_args = a:extra_args->empty()
        \ ? g:goda_pm.settings->get('extra_args', {}) : a:extra_args

  if base_path ==# ''
    call goda_pm#util#_error('goda_pm#make_state() base_path is empty.')
    return 1
  endif

  let g:goda_pm.settings.base_path = base_path
  let g:goda_pm.settings.name = name
  let g:goda_pm.settings.config_dir = exists('g:goda_pm#binary')
        \ ? '' : s:config_dir()

  try
    call s:prepare()

    " Remove old state files
    call goda_pm#util#_clear_state(name)

    const id = s:call('makeState', [#{
          \   base_path: base_path,
          \   name: name,
          \   extra_args: extra_args,
          \ }])
    if a:sync
      call s:wait(id)
    endif
  catch
    call goda_pm#util#_error(v:exception)
    return 1
  endtry
endfunction

function goda_pm#clear_state(name = '') abort
  const name_val = a:name ==# '' ? goda_pm#util#_get_name() : a:name
  call goda_pm#util#_clear_state(name_val)
endfunction

function goda_pm#check_files(base_path = '', name = '') abort
  const has_goda_pm = 'g:goda_pm'->exists()
  const base_path_val = a:base_path ==# ''
        \ ? (has_goda_pm ? g:goda_pm.settings->get('base_path', '') : '')
        \ : a:base_path
  const name_val = a:name ==# '' ? goda_pm#util#_get_name() : a:name
  return goda_pm#util#_check_files(base_path_val, name_val)
endfunction

function goda_pm#check_clean() abort
  return goda_pm#util#_check_clean()
endfunction

" The exts of the binary and their actions: {ext: [action]}.
function goda_pm#exts() abort
  call s:prepare()
  return goda#request('pm', 'exts')
endfunction
