package toml

import (
	"reflect"
	"testing"

	"github.com/Shougo/goda-pm.vim/pm"
)

func TestParse(t *testing.T) {
	r, err := Parse([]byte(`
hooks_file = '$BASE_DIR/hooks.vim'

[ftplugin]
python = '''
setlocal sw=4
'''

[[plugins]]
repo = 'Shougo/goda-fi.vim'
on_cmd = ['GodaFi']
hook_add = '''
let g:x = 1
'''

[[plugins]]
repo = 'Shougo/goda.vim'
name = 'goda'
lazy = false
priority = 10

[[multiple_hooks]]
plugins = ['goda', 'goda-fi.vim']
hook_source = 'let g:y = 1'
`), pm.Plugin{"lazy": true, "depends": "denops.vim"})
	if err != nil {
		t.Fatal(err)
	}
	if r.HooksFile != "$BASE_DIR/hooks.vim" || r.Ftplugins["python"] != "setlocal sw=4\n" {
		t.Errorf("got %+v", r)
	}
	want := []pm.Plugin{
		{"repo": "Shougo/goda-fi.vim", "name": "goda-fi.vim", "on_cmd": []any{"GodaFi"}, "hook_add": "let g:x = 1\n",
			"lazy": true, "depends": "denops.vim"},
		{"repo": "Shougo/goda.vim", "name": "goda", "lazy": false, "priority": int64(10), "depends": "denops.vim"},
	}
	if !reflect.DeepEqual(r.Plugins, want) {
		t.Errorf("plugins: %#v", r.Plugins)
	}
	if !reflect.DeepEqual(r.MultipleHooks, []pm.MultipleHook{{Plugins: []string{"goda", "goda-fi.vim"}, HookSource: "let g:y = 1"}}) {
		t.Errorf("multiple_hooks: %#v", r.MultipleHooks)
	}
	if _, err := Parse([]byte("[[plugins]\n"), nil); err == nil {
		t.Error("no error")
	}
}
