package pm

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func available(ps ...Plugin) *availablePlugins {
	a := &availablePlugins{byName: map[string]Plugin{}}
	for _, p := range ps {
		a.list = append(a.list, p)
		a.byName[p.Name()] = p
	}
	return a
}

func TestLayoutRuntimepath(t *testing.T) {
	dir := t.TempDir()
	withAfter := filepath.Join(dir, "after.vim")
	if err := os.MkdirAll(filepath.Join(withAfter, "after"), 0o755); err != nil {
		t.Fatal(err)
	}
	low := Plugin{"name": "low", "rtp": "/low", "hook_source": "low"}
	high := Plugin{"name": "high", "rtp": "/high", "priority": 10, "depends": []any{"dep", "none"}}
	after := Plugin{"name": "after.vim", "rtp": withAfter}
	dep := Plugin{"name": "dep", "rtp": "/dep", "lazy": true, "hook_source": "dep"}
	merged := Plugin{"name": "merged", "rtp": "/merged", "merged": true}
	lazy := Plugin{"name": "lazy", "rtp": "/lazy", "lazy": true}

	l, err := layoutRuntimepath([]string{"/user", "/runtime", "/user/after"}, "/runtime", "/state/.goda_pm",
		available(low, high, after, dep, merged, lazy))
	if err != nil {
		t.Fatal(err)
	}
	// Each plugin goes just before the runtime: the last one added comes
	// first. Its after directory goes just after the runtime.
	want := []string{"/user", "/dep", withAfter, "/low", "/high", "/state/.goda_pm", "/runtime",
		withAfter + "/after", "/user/after", "/state/.goda_pm/after"}
	if !reflect.DeepEqual(l.rtps, want) {
		t.Errorf("rtps:\n got %q\nwant %q", l.rtps, want)
	}
	if want := []string{"low", "dep"}; !reflect.DeepEqual(l.hookSources, want) {
		t.Errorf("hookSources: got %q, want %q", l.hookSources, want)
	}
	if want := map[string]bool{"high": true, "low": true, "after.vim": true, "merged": true}; !reflect.DeepEqual(l.nonLazy, want) {
		t.Errorf("nonLazy: got %v, want %v", l.nonLazy, want)
	}
	if want := []string{`Not available dependency: "none" in high`}; !reflect.DeepEqual(l.errors, want) {
		t.Errorf("errors: got %q, want %q", l.errors, want)
	}
	for _, p := range []Plugin{low, high, after, dep, merged} {
		if !p.Bool("sourced") {
			t.Errorf("%s: not sourced", p.Name())
		}
	}
	if lazy.Has("sourced") {
		t.Error("lazy: sourced")
	}

	if _, err := layoutRuntimepath([]string{"/user"}, "/runtime", "/state/.goda_pm", available()); err == nil {
		t.Error("no runtime: no error")
	}
}

func TestInlineLines(t *testing.T) {
	vim := "\" comment\r\nset number\n\n  \" indented\nlet g:a = 1"
	if got, want := inlineLines("/a/inline.vim", vim, false), []string{"set number", "let g:a = 1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("vim: got %q, want %q", got, want)
	}
	lua := "-- comment\nvim.o.number = true\n\n"
	if got, want := inlineLines("/a/inline.lua", lua, true), []string{"lua <<EOF", "vim.o.number = true", "EOF"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lua: got %q, want %q", got, want)
	}
	if got := inlineLines("/a/inline.lua", lua, false); got != nil {
		t.Errorf("lua without Lua: got %q", got)
	}
}

func TestSplitMultipleHooks(t *testing.T) {
	avail := map[string]Plugin{"a": {"name": "a"}, "b": {"name": "b"}}
	nonLazy := map[string]bool{"a": true}
	mhs := []MultipleHook{
		{Plugins: []string{"a", "b"}, HookAdd: "add ab", HookSource: "source ab"},
		{Plugins: []string{"a"}, HookSource: "source a", HookPostSource: "post\n  \\ a"},
		{Plugins: []string{"a", "c"}, HookAdd: "add ac"},
	}
	adds, sources, rest := splitMultipleHooks(mhs, avail, nonLazy)
	if want := []string{"add ab"}; !reflect.DeepEqual(adds, want) {
		t.Errorf("adds: got %q, want %q", adds, want)
	}
	if want := []string{"source a"}; !reflect.DeepEqual(sources, want) {
		t.Errorf("sources: got %q, want %q", sources, want)
	}
	want := []MultipleHook{
		{Plugins: []string{"a", "b"}, HookSource: "source ab"},
		{Plugins: []string{"a"}, HookPostSource: "post a"},
		{Plugins: []string{"a", "c"}, HookAdd: "add ac"},
	}
	if !reflect.DeepEqual(rest, want) {
		t.Errorf("rest:\n got %+v\nwant %+v", rest, want)
	}
}

func TestStartupHead(t *testing.T) {
	dir := t.TempDir()
	mod := filepath.Join(dir, "it's")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	head := startupHead(3, "/a,/b'c", []Plugin{{"name": "m", "path": mod}, {"name": "n", "path": dir}})
	if got, want := head[0], `if g:goda_pm._state_version !=# 3 | throw "State version error" | endif`; got != want {
		t.Errorf("version: got %q, want %q", got, want)
	}
	if got, want := head[2], `let &runtimepath = '/a,/b''c'`; got != want {
		t.Errorf("runtimepath: got %q, want %q", got, want)
	}
	if got, want := head[3], "let g:goda#workspace = ['"+strings.ReplaceAll(mod, "'", "''")+"']"; got != want {
		t.Errorf("workspace: got %q, want %q", got, want)
	}
}

func TestConfigCheckFiles(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"b.go", "a.go", "go.mod", "go.sum"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ret := &ConfigReturn{CheckFiles: []string{"plugins.toml"}, HooksFiles: []string{"hooks.vim"}}
	got := configCheckFiles(ret, dir)
	want := []string{"plugins.toml", filepath.Join(dir, "a.go"), filepath.Join(dir, "b.go"), filepath.Join(dir, "go.mod"), "hooks.vim"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := configCheckFiles(ret, ""), []string{"plugins.toml", "hooks.vim"}; !reflect.DeepEqual(got, want) {
		t.Errorf("no config dir: got %q, want %q", got, want)
	}
}
