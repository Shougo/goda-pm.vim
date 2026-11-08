" The end-to-end scenario, as a vimrc: $GODA_PM_PHASE is its phase.
" Failures go to $GODA_PM_RESULT; it says "ok" at the end.
"
"   make:    there is no state; make it.
"   install: start with the state; install the plugins.
"   update:  start with the state; the plugins are loaded; update them.
"   updated: start with the state the update made.

set nocompatible
set encoding=utf-8
set nomore
let s:errors = []
let s:phase = $GODA_PM_PHASE

execute 'set runtimepath^=' . fnameescape($GODA_VIM_DIR)
execute 'set runtimepath^=' . fnameescape($GODA_PM_DIR)
let g:goda_pm#binary = $GODA_PM_BINARY
let g:goda_pm#config_dir = $GODA_PM_CONFIG_DIR
let s:base = $GODA_PM_BASE

function! s:check(name, got, want) abort
  if a:got !=# a:want
    call add(s:errors, printf('%s: got %s, want %s', a:name, string(a:got), string(a:want)))
  endif
endfunction

let g:make_state_post = 0
let g:update_done = 0
augroup test
  autocmd!
  autocmd User GodaPm:makeStatePost let g:make_state_post += 1
  autocmd User GodaPm:ext:installer:updateDone let g:update_done += 1
augroup END

let s:load_state = goda_pm#min#load_state(s:base)

filetype plugin indent on

function! s:names(plugins) abort
  return sort(map(copy(a:plugins), 'v:val.name'))
endfunction

" The file of the help {tag}.
function! s:help(tag) abort
  try
    execute 'help' a:tag
    let file = expand('%:t')
    helpclose
    return file
  catch
    return v:exception
  endtry
endfunction

function! s:make() abort
  call s:check('load_state', s:load_state, 1)
  call goda_pm#make_state_sync()
  call s:check('make_state_post', g:make_state_post, 1)
  call s:check('startup.vim', filereadable(s:base . '/' . goda_pm#util#_get_name() . '/startup.vim'), 1)
endfunction

function! s:install() abort
  call s:check('load_state', s:load_state, 0)
  let not_installed = s:names(goda_pm#sync_ext_action('installer', 'getNotInstalled'))
  call s:check('not installed', not_installed, ['dep.vim', 'foo.vim', 'http-plugin', 'lazy-cmd.vim',
        \ 'lazy-event.vim', 'lazy-ft.vim', 'lazy-func.vim', 'merged.vim', 'skipped.vim'])
  call goda_pm#sync_ext_action('installer', 'install')
  call s:check('update_done', g:update_done, 1)
  call s:check('installed', goda_pm#sync_ext_action('installer', 'getNotInstalled'), [])
  call s:check('failed', goda_pm#sync_ext_action('installer', 'getFailed'), [])
  " The merged plugins are installed: the state is made again.
  call s:check('make_state_post after install', g:make_state_post, 1)
  call s:check('installer log', filereadable($GODA_PM_FILES . '/installer.log'), 1)
endfunction

function! s:update() abort
  call s:check('load_state', s:load_state, 0)
  call s:check('inline vimrc', get(g:, 'inline_vimrc'), 1)
  call s:check('hook_add', get(g:, 'hook_add_foo'), 1)
  call s:check('hook_add of hooks_file', get(g:, 'merged_hook_add'), 1)
  call s:check('foo loaded', get(g:, 'foo_version'), 1)
  call s:check('merged autoload', merged#hello(), 'merged')
  call s:check('http plugin', get(g:, 'http_plugin'), 1)
  call s:check('local plugin', get(g:, 'local_plugin'), 1)
  call s:check('ignored local plugin', exists('g:ignored'), 0)
  call s:check('local overrides', [get(g:, 'bar_local'), exists('g:bar_remote')], [1, 0])
  call s:check('local path', goda_pm#get('bar.vim').path, $GODA_PM_LOCAL->substitute('\\', '/', 'g') . '/bar.vim')
  call s:check('local url', goda_pm#get('bar.vim').url, 'https://example.com/test/bar.vim.git')
  call s:check('if', exists('g:skipped'), 0)
  call s:check('goda#workspace', map(copy(g:goda#workspace), 'fnamemodify(v:val, ":t")'), ['merged.vim'])
  call s:check('help', s:help('foo'), 'foo.txt')
  call s:check('help of a merged plugin', s:help('merged.vim'), 'merged.txt')
  call s:check('help of a README', s:help('lazy-func.vim-Usage'), 'README.md')
  call s:check('check_files', goda_pm#check_files(), [])
  new
  call goda_pm#toml_syntax()
  call s:check('toml_syntax', synIDtrans(hlID('tomlVim')) >= 0 && execute('syntax list tomlVim') =~# 'tomlVim', v:true)
  close
  call s:check('check_clean', goda_pm#check_clean(), [])
  call s:check('get', sort(keys(goda_pm#get())), ['bar.vim', 'dep.vim', 'foo.vim', 'http-plugin',
        \ 'lazy-cmd.vim', 'lazy-event.vim', 'lazy-ft.vim', 'lazy-func.vim', 'local.vim', 'merged.vim',
        \ 'skipped.vim'])

  " Lazy loading.
  call s:check('dummy command', exists(':LazyCmd'), 2)
  call s:check('depends not loaded', exists('g:dep_loaded'), 0)
  LazyCmd
  call s:check('on_cmd', get(g:, 'lazy_cmd'), 1)
  call s:check('depends', get(g:, 'dep_loaded'), 1)
  call s:check('hook_source', get(g:, 'lazy_cmd_source'), 1)
  call s:check('hook_post_source', get(g:, 'lazy_cmd_post_source'), 1)
  call s:check('multiple_hooks', get(g:, 'multiple_source'), 1)
  call s:check('sourced', goda_pm#get('lazy-cmd.vim').sourced, v:true)
  call s:check('on_func', lazy_func#call(), 42)
  call s:check('on_event before', exists('g:lazy_event'), 0)
  doautocmd User LazyEvent
  call s:check('on_event', get(g:, 'lazy_event'), 1)
  call s:check('on_ft before', exists('g:lazy_ft'), 0)
  edit test.mergedft
  call s:check('ftdetect', &filetype, 'mergedft')
  call s:check('on_ft', get(g:, 'lazy_ft'), 1)
  call s:check('ftplugin', get(b:, 'ftplugin_mergedft'), 1)
  call s:check('ftplugin of hooks file', get(b:, 'hooks_ftplugin'), 1)

  if has('nvim')
    call s:check('lua get', luaeval("require('goda_pm').get('foo.vim').name"), 'foo.vim')
    call s:check('lua check_clean', luaeval("#require('goda_pm').check_clean()"), 0)
  endif

  " Update: foo.vim has a new commit.
  call s:check('exts', goda_pm#exts().installer->index('update') >= 0, v:true)
  " hook_post_update builds goda-pm: after the update.
  let g:test_build = 1
  let bin_time = getftime(g:goda_pm#binary)
  call goda_pm#sync_ext_action('installer', 'update')
  call s:check('update_done', g:update_done, 1)
  " (The build may have restarted the binary already: the log of the
  " installer, not getUpdated.)
  let log = readfile($GODA_PM_FILES . '/installer.log')
  let last = len(log) - 1 - index(reverse(copy(log)), 'Updated plugins:')
  call s:check('updated', log[last + 1 :], ['  foo.vim(1 change)'] + log[last + 2 :])
  call s:check('hook_post_update', get(g:, 'post_update_foo'), 1)
  " In the directory of the plugin, and back.
  call s:check('hook directory', fnamemodify(get(g:, 'post_update_cwd', ''), ':t'), 'foo.vim')
  call s:check('directory back', getcwd() !=# get(g:, 'post_update_cwd', ''), v:true)
  call s:check('messages', execute('messages') =~# '\[goda-pm\] Updated plugins:', v:true)
  call s:check('rollback', filereadable(s:base . '/' . goda_pm#util#_get_name()
        \ . '/rollbacks/latest/rollback.json'), 1)
  " The documents changed: the state is made again.
  call s:check('make_state_post', g:make_state_post, 1)
  call s:check('unknown action', goda_pm#sync_ext_action('installer', 'nope'), 0)

  " The build after the update.
  let waited = 0
  while execute('messages') !~# 'goda-pm: built' && waited < 120000
    sleep 100m
    let waited += 100
  endwhile
  let messages = execute('messages')
  call s:check('build waits', messages =~# 'goda-pm: the build waits for the running actions', 1)
  call s:check('built', messages =~# 'goda-pm: built', 1)
  call s:check('binary replaced', getftime(g:goda_pm#binary) >= bin_time, 1)
  call s:check('new binary', goda_pm#exts().installer->index('update') >= 0, v:true)
endfunction

function! s:updated() abort
  call s:check('load_state', s:load_state, 0)
  call s:check('updated plugin', get(g:, 'foo_version'), 2)
  call s:check('new help', s:help('foo-new'), 'foo.txt')
endfunction

function! s:main() abort
  try
    call call('s:' . s:phase, [])
  catch
    call add(s:errors, 'exception: ' . v:exception . ' at ' . v:throwpoint)
  endtry
  if !empty(s:errors)
    let s:errors += ['messages:'] + split(execute('messages'), "\n")
    let s:errors += ['goda#log():'] + goda#log()
    let s:errors += ['rtp: ' . &runtimepath]
    let s:errors += ['files: '] + glob(s:base . '/*/.goda_pm/**', v:true, v:true)
  endif
  call writefile(empty(s:errors) ? ['ok'] : s:errors, $GODA_PM_RESULT)
  qall!
endfunction

autocmd test VimEnter * ++once ++nested call s:main()
