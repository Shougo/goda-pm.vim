package pm

import (
	"reflect"
	"testing"
)

func TestParseHooksFile(t *testing.T) {
	tests := []struct {
		lines []string
		want  Plugin
	}{
		{[]string{`" c {{{`, "hogera", "}}}", `" hook_source {{{`, "piyo", "}}}"},
			Plugin{"hook_source": "piyo", "ftplugin": map[string]string{"c": "hogera"}}},
		{[]string{`" {{{`, "hogera", "}}}", `" hook_source {{{`, "piyo", "}}}"},
			Plugin{"hook_source": "piyo", "ftplugin": map[string]string{}}},
		{[]string{`" hook_source {{{`, "piyo", `" {{{`, "hogera", `" }}}`, `" }}}`},
			Plugin{"hook_source": "piyo\n\" {{{\nhogera\n\" }}}", "ftplugin": map[string]string{}}},
		{[]string{`" hook_source {{{`, `" }}}`, "", `" hook_add {{{`, " comment1 {{{", "hoge", `" }}}`, "",
			" comment2 {{{", "fuga", `" }}}`, `" }}}`},
			Plugin{"hook_add": " comment1 {{{\nhoge\n\" }}}\n\n comment2 {{{\nfuga\n\" }}}", "ftplugin": map[string]string{}}},
		{[]string{"-- lua_source {{{", "piyo", "-- }}}"},
			Plugin{"lua_source": "piyo", "ftplugin": map[string]string{}}},
		{[]string{"-- lua_hoge {{{", "piyo", "-- }}}"},
			Plugin{"ftplugin": map[string]string{"lua_hoge": "piyo"}}},
	}
	for i, tt := range tests {
		got, err := ParseHooksFile("{{{,}}}", tt.lines, "")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%d: got %#v, want %#v", i, got, tt.want)
		}
	}
	if _, err := ParseHooksFile("{{{,}}}", []string{`" hook_add {{{`, "foo"}, "x.vim"); err == nil {
		t.Error("an unterminated block is not an error")
	}
}

func TestMergeFtplugins(t *testing.T) {
	dest := map[string]string{"c": "a"}
	MergeFtplugins(dest, map[string]string{"c_cpp": "b", "_": "c", "lua_python": "d"})
	want := map[string]string{"c": "a\nb", "cpp": "b", "_": "c", "python": "lua <<EOF\nd\nEOF\n"}
	if !reflect.DeepEqual(dest, want) {
		t.Errorf("got %#v", dest)
	}
}

func TestInitPlugin(t *testing.T) {
	tests := []struct {
		in     Plugin
		hasLua bool
		want   Plugin
	}{
		{Plugin{"name": "foo", "rev": "[hoge]", "path": "repos/foo", "script_type": "foo", "rtp": "autoload"}, false,
			Plugin{"name": "foo", "path": "base/repos/foo__hoge_/foo", "rtp": "base/repos/foo__hoge_/foo/autoload",
				"rev": "[hoge]", "script_type": "foo", "lazy": false, "merged": true, "sourced": false}},
		{Plugin{"name": "foo", "path": "repos/foo", "on_ft": "foo"}, false,
			Plugin{"name": "foo", "path": "base/repos/foo", "rtp": "base/repos/foo", "lazy": true, "merged": false,
				"on_ft": "foo", "sourced": false}},
		{Plugin{"name": "foo", "path": "repos/foo", "lua_add": "foo"}, true,
			Plugin{"name": "foo", "path": "base/repos/foo", "rtp": "base/repos/foo", "lazy": false, "merged": true,
				"sourced": false, "hook_add": "lua <<EOF\nfoo\nEOF\n", "lua_add": "foo"}},
		{Plugin{"name": "foo", "path": "/abs/foo/", "rtp": "", "depends": "bar", "hook_add": "a\n  \\ b"}, false,
			Plugin{"name": "foo", "path": "/abs/foo/", "rtp": "", "lazy": false, "merged": true, "sourced": false,
				"depends": []any{"bar"}, "hook_add": "a b"}},
	}
	for i, tt := range tests {
		if got := InitPlugin(tt.in, "base", tt.hasLua); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%d: got %#v, want %#v", i, got, tt.want)
		}
	}
}

func TestDetectTagsInMarkdown(t *testing.T) {
	got := detectTagsInMarkdown("# foo.vim\n\n## Install it\ntext\n<h2>Usage</h2>\n")
	want := []markdownTag{
		{"foo.vim", `/#\s\+foo\.vim`},
		{"Install-it", `/##\s\+Install\s\+it`},
		{"Usage", `/<h2>Usage<\/h2>`},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v", got)
	}
}

func TestPluginValues(t *testing.T) {
	p := Plugin{"a": "x", "b": []any{"y", "z"}, "n": int64(3), "f": float64(0), "t": true}
	if got := p.Strings("a"); !reflect.DeepEqual(got, []string{"x"}) {
		t.Errorf("Strings(a): %v", got)
	}
	if got := p.Strings("b"); !reflect.DeepEqual(got, []string{"y", "z"}) {
		t.Errorf("Strings(b): %v", got)
	}
	if p.Strings("none") != nil || p.Int("n") != 3 || p.Bool("f") || !p.Bool("t") || !p.Bool("n") {
		t.Error("values")
	}
	if got := Merge(Plugin{"a": 1, "b": 1}, Plugin{"b": 2}); !reflect.DeepEqual(got, Plugin{"a": 1, "b": 2}) {
		t.Errorf("Merge: %v", got)
	}
}

func TestDecodeParams(t *testing.T) {
	type params struct {
		A int    `json:"a"`
		B string `json:"b"`
	}
	p := params{A: 1, B: "x"}
	if err := DecodeParams(Params{"b": "y", "unknown": 1}, &p); err != nil {
		t.Fatal(err)
	}
	if p != (params{A: 1, B: "y"}) {
		t.Errorf("got %+v", p)
	}
}
