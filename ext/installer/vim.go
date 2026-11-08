package installer

import (
	"fmt"

	"github.com/Shougo/goda-pm.vim/pm"
)

// The editor side of the installer: functions of Vim and Neovim, and
// goda_pm#util#_print_message() (Vim has no function for :echomsg).

func (r *run) call(fn string, args ...any) (any, error) {
	return r.a.Host.Call(r.ctx, fn, args...)
}

func (r *run) truthy(v any, err error) bool {
	if err != nil {
		return false
	}
	switch n := v.(type) {
	case bool:
		return n
	case int64:
		return n != 0
	case uint64:
		return n != 0
	case float64:
		return n != 0
	}
	return false
}

func (r *run) number(v any, err error) int64 {
	if err != nil {
		return 0
	}
	switch n := v.(type) {
	case int64:
		return n
	case uint64:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}

// headless: Vim or Neovim runs from a shell (no UI), as in a cron job.
func (r *run) headless() bool {
	r.vimOnce.Do(func() {
		r.isNvim = r.truthy(r.call("has", "nvim"))
		r.isHeadless = r.truthy(r.call("eval",
			"has('nvim') ? nvim_list_uis()->empty() : mode(v:true) ==# 'ce'"))
	})
	return r.isHeadless
}

func (r *run) printMessage(msg string) {
	_, _ = r.call("goda_pm#util#_print_message", msg)
	r.log(msg, true)
}

// printProgress shows msg in the progress window (in a shell, as a
// message).
func (r *run) printProgress(msg string) {
	defer r.log(msg, false)
	if r.headless() {
		_, _ = r.call("goda_pm#util#_print_message", msg)
		return
	}
	r.progressMu.Lock()
	defer r.progressMu.Unlock()
	if r.progressWin <= 0 {
		r.progressWin = r.newProgressWindow()
	}
	if r.progressWin <= 0 {
		return
	}
	buf := r.number(r.call("winbufnr", r.progressWin))
	if buf <= 0 {
		return
	}
	if first, err := r.call("getbufline", buf, 1); err == nil && isEmptyLine(first) {
		_, _ = r.call("setbufline", buf, 1, msg)
	} else {
		_, _ = r.call("appendbufline", buf, "$", msg)
	}
	_, _ = r.call("win_execute", r.progressWin, "call cursor('$', 0) | redraw")
}

func isEmptyLine(v any) bool {
	l, ok := v.([]any)
	return ok && len(l) == 1 && l[0] == ""
}

func (r *run) newProgressWindow() int64 {
	cols := r.number(r.call("eval", "&columns"))
	row, col, width, height := 0, cols/4, 80, 20
	if r.isNvim {
		buf := r.number(r.call("nvim_create_buf", false, true))
		return r.number(r.call("nvim_open_win", buf, false, map[string]any{
			"border":    "none",
			"relative":  "editor",
			"row":       row,
			"col":       col,
			"focusable": false,
			"noautocmd": true,
			"style":     "minimal",
			"width":     width,
			"height":    height,
		}))
	}
	return r.number(r.call("popup_create", []any{}, map[string]any{
		"pos":       "topleft",
		"line":      row + 1,
		"col":       col + 1,
		"minwidth":  width,
		"minheight": height,
		"wrap":      0,
	}))
}

func (r *run) closeProgressWindow() {
	r.progressMu.Lock()
	defer r.progressMu.Unlock()
	if r.progressWin <= 0 {
		return
	}
	if r.number(r.call("winbufnr", r.progressWin)) >= 0 {
		if r.isNvim {
			_, _ = r.call("execute", fmt.Sprintf("silent! call nvim_win_close(%d, v:true)", r.progressWin))
		} else {
			_, _ = r.call("popup_close", r.progressWin)
		}
	}
	r.progressWin = -1
}

// outputDiff shows the changes of the documents of the updated plugins
// (checkDiff) in the buffer goda-pm-diff.
func (r *run) outputDiff(lines []string) {
	if len(lines) == 0 {
		return
	}
	const name = "goda-pm-diff"
	var buf int64
	if r.truthy(r.call("bufexists", name)) {
		buf = r.number(r.call("bufnr", name))
	} else {
		buf = r.number(r.call("bufadd", name))
	}
	if buf <= 0 {
		return
	}
	if r.number(r.call("bufwinnr", buf)) < 0 {
		win := r.number(r.call("win_getid"))
		_, _ = r.call("execute", []any{
			fmt.Sprintf("sbuffer %d", buf),
			"setlocal bufhidden=wipe filetype=diff syntax=diff buftype=nofile nolist",
		})
		_, _ = r.call("win_gotoid", win)
	}
	_, _ = r.call("setbufvar", buf, "&modifiable", true)
	_, _ = r.call("appendbufline", buf, "$", lines)
	_, _ = r.call("setbufvar", buf, "&modifiable", false)
}

// callHook calls the hook_{name} of p, in its directory.
func (r *run) callHook(name string, p pm.Plugin, args ...any) {
	if !p.Has("hook_" + name) {
		return
	}
	hookArgs := any(map[string]any{})
	if len(args) > 0 {
		hookArgs = args[0]
	}
	err := func() error {
		if _, err := r.call("goda_pm#source", p.Name()); err != nil {
			return err
		}
		cwd, _ := r.call("getcwd")
		r.changeDirectory(p.String("path"))
		if s, ok := cwd.(string); ok {
			defer r.changeDirectory(s)
		}
		_, err := r.call("goda_pm#util#_call_hook", name, p.Name(), hookArgs)
		return err
	}()
	if err != nil {
		r.printError(fmt.Sprintf("%s: hook_%s: %v", p.Name(), name, err))
	}
}

func (r *run) changeDirectory(path string) {
	if path == "" || !r.truthy(r.call("isdirectory", path)) {
		return
	}
	escaped, err := r.call("fnameescape", path)
	if err != nil {
		return
	}
	cmd := "cd"
	if r.truthy(r.call("haslocaldir")) {
		cmd = "lcd"
	}
	if _, err := r.call("execute", fmt.Sprintf("noautocmd %s %s", cmd, escaped)); err != nil {
		cwd, _ := r.call("getcwd")
		_, _ = r.call("goda_pm#util#_error", fmt.Sprintf(
			"Error cd to: %s\nCurrent directory: %v\n%v", path, cwd, err))
	}
}
