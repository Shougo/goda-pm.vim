function goda_pm#min#load_state(path, name=v:progname->fnamemodify(':r')) abort
  if !'#goda_pm'->exists()
    call goda_pm#min#_init()
  endif
  if g:goda_pm._is_sudo | return 1 | endif
  let g:goda_pm.settings.base_path = a:path->expand()
  let g:goda_pm.settings.name = a:name
  const startup = printf('%s/%s/startup.vim',
        \ g:goda_pm.settings.base_path, a:name)
  const state = printf('%s/%s/state.vim',
        \ g:goda_pm.settings.base_path, a:name)
  if !startup->filereadable() || !state->filereadable() | return 1 | endif
  try
    let g:goda_pm.cache._state =
          \   has('nvim')
          \ ? state->readfile()->json_decode()
          \ : state->readfile()[0]->js_decode()
    execute 'source' startup->fnameescape()
    unlet g:goda_pm.cache._state
  catch
    call goda_pm#util#_error('Loading state error: ' .. v:exception)
    call goda_pm#util#_clear_state(a:name)
    return 1
  endtry
endfunction
function goda_pm#min#_init() abort
  " Initialize the unified g:goda_pm namespace with structured sections
  " Skip if already initialized
  if exists('g:goda_pm') && has_key(g:goda_pm, '_state_version')
    return
  endif

  let g:goda_pm = {}

  " Settings: Configuration options
  let g:goda_pm.settings = {}

  " State: Dynamic plugin states
  let g:goda_pm.state = {}
  let g:goda_pm.state.plugins = {}
  let g:goda_pm.state.options = {}
  let g:goda_pm.state.check_files = []
  let g:goda_pm.state.multiple_hooks = []

  " Cache: Temporary cache data
  let g:goda_pm.cache = {}

  " Version and initialization flags (at root level)
  let g:goda_pm._state_version = 1
  let g:goda_pm._is_sudo =
        \    $SUDO_USER !=# '' && $USER !=# $SUDO_USER
        \ && $HOME !=# ('~'.$USER)->expand()
        \ && $HOME ==# ('~'.$SUDO_USER)->expand()
  let g:goda_pm._init_runtimepath = &runtimepath
  let g:goda_pm._did_load_filetypes =
        \    g:->get('did_load_filetypes', v:false)
        \ || has('nvim')
  let g:goda_pm._did_load_ftplugin =
        \    b:->get('did_ftplugin', v:false)
        \ || b:->get('did_indent', v:false)
        \ || has('nvim')

  let g:goda_pm#hook_result = v:null
  let g:goda_pm#hook_args = v:null

  augroup goda_pm
    autocmd!
  augroup END
endfunction
