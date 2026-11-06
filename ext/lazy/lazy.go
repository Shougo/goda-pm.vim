// Package lazy is the lazy loading of goda-pm.vim, as dpp-ext-lazy: the
// plugins with on_* keys (on_cmd, on_ft, on_map, on_event, on_func, on_lua,
// on_path, on_root, on_if, on_source, ...) are loaded when they are needed.
//
// Give the plugins to MakeState in the configuration, and return what it
// gives:
//
//	r, err := lazy.MakeState(ctx, c.Host, plugins)
//	...
//	return &pm.ConfigReturn{Plugins: r.Plugins, StateLines: r.StateLines, ...}, nil
package lazy

import (
	"context"
	"fmt"

	"github.com/Shougo/goda-pm.vim/pm"
	"github.com/Shougo/goda.vim/sdk"
)

// Result is what MakeState gives: the plugins (with their dummy commands and
// mappings) and the lines of the startup script that load them.
type Result struct {
	Plugins    []pm.Plugin
	StateLines []string
}

// stateLines define the autocommands of the lazy loading.
var stateLines = []string{
	"augroup goda_pm-ext-lazy",
	"  autocmd!",
	"  autocmd FuncUndefined *",
	"       \\ call goda_pm#ext#lazy#_on_func('<afile>'->expand())",
	" autocmd BufRead *? call goda_pm#ext#lazy#_on_default_event('BufRead')",
	" autocmd BufNew,BufNewFile *? call goda_pm#ext#lazy#_on_default_event('BufNew')",
	" autocmd VimEnter *? call goda_pm#ext#lazy#_on_default_event('VimEnter')",
	" autocmd FileType *? call goda_pm#ext#lazy#_on_default_event('FileType')",
	" autocmd CmdUndefined * call goda_pm#ext#lazy#_on_pre_cmd('<afile>'->expand())",
	" autocmd BufRead,DirChanged * call goda_pm#ext#lazy#_on_root()",
	"augroup END",
	"augroup goda_pm-ext-lazy-on_event",
	"augroup END",
	"let g:goda_pm#ext#_called_vim = {}",
	"if has('nvim')",
	"let g:goda_pm#ext#_on_lua_plugins = {}",
	"let g:goda_pm#ext#_called_lua = {}",
	"lua <<END",
	"table.insert(package.loaders, 1, (function()",
	"  return function(mod_name)",
	"    local mod_root = string.match(mod_name, '^[^./]+')",
	"    if vim.g['goda_pm#ext#_on_lua_plugins'][mod_root] then",
	"      vim.fn['goda_pm#ext#lazy#_on_lua'](mod_name, mod_root)",
	"    end",
	"    if package.loaded[mod_name] ~= nil then",
	"      local m = package.loaded[mod_name]",
	"      return function()",
	"        return m",
	"      end",
	"    end",
	"    return nil",
	"  end",
	"end)())",
	"END",
	"endif",
}

type dummys[T any] struct {
	Dummys     []T      `json:"dummys"`
	StateLines []string `json:"stateLines"`
}

func isLazy(p pm.Plugin) bool {
	if p.Bool("lazy") {
		return true
	}
	for k := range p {
		if len(k) > 3 && k[:3] == "on_" {
			return true
		}
	}
	return false
}

// MakeState gives the plugins their dummy commands and mappings, and the
// lines that load them, as dpp-ext-lazy's makeState action. The plugins are
// changed (dummy_commands, dummy_mappings) and given back.
func MakeState(ctx context.Context, h sdk.Host, plugins []pm.Plugin) (Result, error) {
	lines := append([]string{}, stateLines...)
	var lazy []pm.Plugin
	for _, p := range plugins {
		if isLazy(p) {
			lazy = append(lazy, p)
		}
	}
	printError := func(format string, args ...any) {
		_ = h.Notify(ctx, "goda_pm#util#_error", fmt.Sprintf(format, args...))
	}

	lines = append(lines, "function! s:define_on_map() abort")
	maps := map[string]map[string]bool{}
	for _, p := range lazy {
		v, err := h.Call(ctx, "goda_pm#ext#lazy#_generate_dummy_mappings", p)
		if err != nil {
			return Result{}, err
		}
		var d dummys[[2]string]
		if err := sdk.Decode(v, &d); err != nil {
			return Result{}, err
		}
		lines = append(lines, d.StateLines...)
		if len(d.Dummys) > 0 {
			var list []any
			for _, m := range d.Dummys {
				list = append(list, []any{m[0], m[1]})
				if maps[m[0]] == nil {
					maps[m[0]] = map[string]bool{}
				}
				if maps[m[0]][m[1]] {
					printError("Duplicated on_map is detected: %q for mode %q in %q", m[1], m[0], p.Name())
				}
				maps[m[0]][m[1]] = true
			}
			p["dummy_mappings"] = list
		}
	}
	lines = append(lines,
		"endfunction",
		"autocmd goda_pm-ext-lazy SafeState * ++once call s:define_on_map()",
	)

	var events []string
	seenEvent := map[string]bool{}
	commands := map[string]bool{}
	for _, p := range lazy {
		v, err := h.Call(ctx, "goda_pm#ext#lazy#_generate_dummy_commands", p)
		if err != nil {
			return Result{}, err
		}
		var d dummys[string]
		if err := sdk.Decode(v, &d); err != nil {
			return Result{}, err
		}
		if len(d.Dummys) > 0 {
			var list []any
			for _, c := range d.Dummys {
				list = append(list, c)
				if commands[c] {
					printError("Duplicated on_cmd is detected: %q in %q", c, p.Name())
				}
				commands[c] = true
			}
			p["dummy_commands"] = list
		}
		lines = append(lines, d.StateLines...)
		if p.Has("on_lua") {
			v, err := h.Call(ctx, "goda_pm#ext#lazy#_generate_on_lua", p)
			if err != nil {
				return Result{}, err
			}
			var l []string
			if err := sdk.Decode(v, &l); err != nil {
				return Result{}, err
			}
			lines = append(lines, l...)
		}
		for _, e := range p.Strings("on_event") {
			if !seenEvent[e] {
				seenEvent[e] = true
				events = append(events, e)
			}
		}
	}
	for _, e := range events {
		v, err := h.Call(ctx, "exists", "##"+e)
		if err != nil {
			return Result{}, err
		}
		if pm.Truthy(v) {
			lines = append(lines, fmt.Sprintf("autocmd goda_pm-ext-lazy-on_event %s * call goda_pm#ext#lazy#_on_event('%s')", e, e))
		} else {
			lines = append(lines, fmt.Sprintf("autocmd goda_pm-ext-lazy-on_event User %s call goda_pm#ext#lazy#_on_event('%s')", e, e))
		}
	}
	return Result{Plugins: plugins, StateLines: lines}, nil
}
