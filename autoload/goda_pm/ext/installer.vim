function goda_pm#ext#installer#_print_message(msg) abort
  const detect_shell = s:is_headless_mode()

  for mes in s:msg2list(a:msg)
    echomsg '[goda-pm] ' .. mes

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
function goda_pm#ext#installer#_print_progress_message(msg) abort
  if s:is_headless_mode()
    return goda_pm#ext#installer#_print_message(a:msg)
  endif

  const winid = s:get_progress_winid()
  if winid <= 0
    return
  endif

  call s:append_progress_message(winid, a:msg)
endfunction
function s:get_progress_winid() abort
  if !exists('s:progress_winid') || s:progress_winid <= 0
    let s:progress_winid = s:new_progress_window()
  endif

  return s:progress_winid
endfunction
function s:append_progress_message(winid, msg) abort
  const bufnr = a:winid->winbufnr()

  if bufnr <= 0
    return
  endif

  const first_line = bufnr->getbufline(1)
  if first_line ==# ['']
    call setbufline(bufnr, 1, a:msg)
  else
    call appendbufline(bufnr, '$', a:msg)
  endif

  call win_execute(a:winid, "call cursor('$', 0) | redraw")
endfunction
function s:msg2list(expr) abort
  return a:expr->type() ==# v:t_list ? a:expr : a:expr->split('\n')
endfunction

function! s:is_headless_mode() abort
  return (has('nvim') && nvim_list_uis()->empty())
          \ || (!has('nvim') && mode(v:true) ==# 'ce')
endfunction

function goda_pm#ext#installer#_close_progress_window() abort
  if !exists('s:progress_winid') || s:progress_winid->winbufnr() < 0
    return
  endif

  if has('nvim')
    silent! call nvim_win_close(s:progress_winid, v:true)
  else
    silent! call popup_close(s:progress_winid)
  endif

  let s:progress_winid = -1
endfunction

function s:new_progress_window() abort
  const winrow = 0
  const wincol = &columns / 4
  const winwidth = 80
  const winheight = 20

  if has('nvim')
    const winid = nvim_open_win(nvim_create_buf(v:false, v:true), v:false, #{
          \   border: 'none',
          \   relative: 'editor',
          \   row: winrow,
          \   col: wincol,
          \   focusable: v:false,
          \   noautocmd: v:true,
          \   style: 'minimal',
          \   width: winwidth,
          \   height: winheight,
          \})
  else
    const winid = popup_create([], #{
          \   pos: 'topleft',
          \   line: winrow + 1,
          \   col: wincol + 1,
          \   minwidth: winwidth,
          \   minheight: winheight,
          \   wrap: 0,
          \ })
  endif

  return winid
endfunction

function goda_pm#ext#installer#_call_hook(hook_name, plugin, args = {}) abort
  const hook = 'hook_' .. a:hook_name

  " Check hook exists
  if !a:plugin->has_key(hook)
    return
  endif

  call goda_pm#source(a:plugin.name)

  const cwd = getcwd()
  try
    call s:change_directory(a:plugin.path)
    call goda_pm#util#_call_hook(a:hook_name, a:plugin.name, a:args)
  finally
    call s:change_directory(cwd)
  endtry
endfunction

function s:change_directory(path) abort
  if !(a:path->isdirectory())
    return
  endif

  try
    noautocmd execute (haslocaldir() ? 'lcd' : 'cd') a:path->fnameescape()
  catch
    call goda_pm#util#_error('Error cd to: ' .. a:path)
    call goda_pm#util#_error('Current directory: ' .. getcwd())
    call goda_pm#util#_error(v:exception)
    call goda_pm#util#_error(v:throwpoint)
  endtry
endfunction

" Show the changes of the documents of the updated plugins (checkDiff).
function goda_pm#ext#installer#_output_diff(lines) abort
  if a:lines->empty()
    return
  endif

  const bufname = 'goda-pm-diff'
  const bufnr = bufname->bufexists() ? bufname->bufnr() : bufname->bufadd()
  if bufnr->bufwinnr() < 0
    const winid = win_getid()
    execute 'sbuffer' bufnr
    setlocal bufhidden=wipe filetype=diff syntax=diff buftype=nofile nolist
    call win_gotoid(winid)
  endif

  call setbufvar(bufnr, '&modifiable', v:true)
  call appendbufline(bufnr, '$', a:lines)
  call setbufvar(bufnr, '&modifiable', v:false)
endfunction
