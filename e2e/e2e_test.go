// Package e2e runs goda-pm in a real Vim and Neovim: the vimrc
// testdata/vimrc.vim, with the binary of testbin, installs plugins from git
// repositories of a temporary directory and from an HTTP server, starts
// with the state, and updates them.
package e2e

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// editor is the executable of Vim or Neovim: $GODA_VIM or $GODA_NVIM, or
// the one in PATH.
func editor(t *testing.T, env, name string) string {
	t.Helper()
	if p := os.Getenv(env); p != "" {
		return p
	}
	p, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("no %s", name)
	}
	return p
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

type env struct {
	dir  string
	vars []string
}

func (e *env) git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = e.vars
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// repo makes the git repository test/name of the remotes, with the files.
func (e *env) repo(t *testing.T, name string, files map[string]string) {
	t.Helper()
	src := filepath.Join(e.dir, "src", name)
	for f, c := range files {
		write(t, filepath.Join(src, f), c)
	}
	e.git(t, src, "init", "-q", "-b", "main")
	e.git(t, src, "add", ".")
	e.git(t, src, "commit", "-q", "-m", "Initial")
	e.git(t, e.dir, "clone", "-q", "--bare", src, filepath.Join(e.dir, "remotes", "test", name+".git"))
	e.git(t, src, "remote", "add", "origin", filepath.Join(e.dir, "remotes", "test", name+".git"))
}

// commit changes the files of the repository and pushes them.
func (e *env) commit(t *testing.T, name string, files map[string]string) {
	t.Helper()
	src := filepath.Join(e.dir, "src", name)
	for f, c := range files {
		write(t, filepath.Join(src, f), c)
	}
	e.git(t, src, "add", ".")
	e.git(t, src, "commit", "-q", "-m", "Update")
	e.git(t, src, "push", "-q", "origin", "main")
}

// setup makes the plugins, the files of the configuration and the binary.
func setup(t *testing.T) *env {
	t.Helper()
	if testing.Short() {
		t.Skip("-short")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	e := &env{dir: dir}
	slash := filepath.ToSlash(dir)
	gitconfig := filepath.Join(dir, "gitconfig")
	// The URL: file:///path on Unix, file://C:/path on MS-Windows.
	url := "file://" + slash
	if !strings.HasPrefix(slash, "/") {
		url = "file:///" + slash
	}
	write(t, gitconfig, fmt.Sprintf("[user]\n\tname = goda-pm\n\temail = goda-pm@example.com\n"+
		"[init]\n\tdefaultBranch = main\n[url %q]\n\tinsteadOf = https://example.com/\n", url+"/remotes/"))
	e.vars = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+gitconfig,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	)

	e.repo(t, "foo.vim", map[string]string{
		"plugin/foo.vim": "let g:foo_version = 1\n",
		"doc/foo.txt":    "*foo.txt*\n\n*foo*\n",
	})
	e.repo(t, "merged.vim", map[string]string{
		"autoload/merged.vim": "function merged#hello() abort\n  return 'merged'\nendfunction\n",
		"doc/merged.txt":      "*merged.txt*\n\n*merged.vim*\n",
		"ftdetect/merged.vim": "autocmd BufRead,BufNewFile *.mergedft setfiletype mergedft\n",
		"README.md":           "# merged.vim\n",
	})
	e.repo(t, "bar.vim", map[string]string{"plugin/bar.vim": "let g:bar_remote = 1\n"})
	e.repo(t, "skipped.vim", map[string]string{"plugin/skipped.vim": "let g:skipped = 1\n"})
	e.repo(t, "lazy-cmd.vim", map[string]string{
		"plugin/lazy_cmd.vim": "command! LazyCmd let g:lazy_cmd = 1\n",
	})
	e.repo(t, "dep.vim", map[string]string{"plugin/dep.vim": "let g:dep_loaded = 1\n"})
	e.repo(t, "lazy-ft.vim", map[string]string{"plugin/lazy_ft.vim": "let g:lazy_ft = 1\n"})
	e.repo(t, "lazy-func.vim", map[string]string{
		"autoload/lazy_func.vim": "function lazy_func#call() abort\n  return 42\nendfunction\n",
		"README.md":              "# lazy-func.vim\n\n## Usage\n",
	})
	e.repo(t, "lazy-event.vim", map[string]string{"plugin/lazy_event.vim": "let g:lazy_event = 1\n"})

	// The plugin of the HTTP server: a zip of GitHub's shape.
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	w, _ := zw.Create("http-plugin-main/plugin/http_plugin.vim")
	w.Write([]byte("let g:http_plugin = 1\n"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zipData := zb.Bytes()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test/http-plugin/archive/main.zip" {
			http.NotFound(w, r)
			return
		}
		w.Write(zipData)
	}))
	t.Cleanup(srv.Close)
	sum := sha1.Sum(zipData)

	local := filepath.Join(dir, "work")
	write(t, filepath.Join(local, "bar.vim", "plugin", "bar.vim"), "let g:bar_local = 1\n")
	write(t, filepath.Join(local, "local.vim", "plugin", "local.vim"), "let g:local_plugin = 1\n")
	write(t, filepath.Join(local, "ignored", "plugin", "ignored.vim"), "let g:ignored = 1\n")

	files := filepath.Join(dir, "files")
	write(t, filepath.Join(files, "inline.vim"), "\" comment\nlet g:inline_vimrc = 1\n")
	write(t, filepath.Join(files, "hooks.vim"), "\" mergedft {{{\nlet b:hooks_ftplugin = 1\n\" }}}\n")
	write(t, filepath.Join(files, "merged_hooks.vim"), "\" hook_add {{{\nlet g:merged_hook_add = 1\n\" }}}\n")
	write(t, filepath.Join(files, "plugins.toml"), fmt.Sprintf(`
hooks_file = '$GODA_PM_FILES/hooks.vim'

[[plugins]]
repo = 'example.com/test/foo.vim'
hook_add = 'let g:hook_add_foo = 1'
hook_post_update = '''
let g:post_update_foo = get(g:, 'post_update_foo', 0) + 1
'''

[[plugins]]
repo = 'example.com/test/merged.vim'
hooks_file = '$GODA_PM_FILES/merged_hooks.vim'

[[plugins]]
repo = 'example.com/test/bar.vim'

[[plugins]]
repo = 'example.com/test/skipped.vim'
if = '0'

[[plugins]]
repo = '%s/test/http-plugin/archive/main.zip'
rev = '%s'
`, srv.URL, hex.EncodeToString(sum[:])))
	write(t, filepath.Join(files, "lazy.toml"), `
[[plugins]]
repo = 'example.com/test/lazy-cmd.vim'
on_cmd = 'LazyCmd'
depends = 'dep.vim'
hook_source = 'let g:lazy_cmd_source = 1'
hook_post_source = 'let g:lazy_cmd_post_source = 1'

[[plugins]]
repo = 'example.com/test/dep.vim'

[[plugins]]
repo = 'example.com/test/lazy-ft.vim'
on_ft = 'mergedft'

[[plugins]]
repo = 'example.com/test/lazy-func.vim'
on_func = 'lazy_func#'

[[plugins]]
repo = 'example.com/test/lazy-event.vim'
on_event = 'LazyEvent'

[ftplugin]
mergedft = 'let b:ftplugin_mergedft = 1'

[[multiple_hooks]]
plugins = ['lazy-cmd.vim', 'dep.vim']
hook_source = 'let g:multiple_source = 1'
`)

	bin := filepath.Join(dir, "goda-pm")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, "./testbin").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/Shougo/goda.vim").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	e.vars = append(e.vars,
		"GODA_VIM_DIR="+strings.TrimSpace(string(out)),
		"GODA_PM_DIR="+root,
		"GODA_PM_BINARY="+bin,
		"GODA_PM_FILES="+files,
		"GODA_PM_LOCAL="+local,
		"GODA_PM_BASE="+filepath.Join(dir, "base"),
		"GODA_PM_RESULT="+filepath.Join(dir, "result"),
	)
	return e
}

// run runs the phase of the scenario in the editor.
func (e *env) run(t *testing.T, phase, name string, args ...string) {
	t.Helper()
	result := filepath.Join(e.dir, "result")
	os.Remove(result)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(e.vars, "GODA_PM_PHASE="+phase)
	out, err := cmd.CombinedOutput()
	b, rerr := os.ReadFile(result)
	if rerr != nil {
		t.Fatalf("%s: no result (%v): %v\n%s", phase, err, rerr, out)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	for _, l := range lines {
		if l == "ok" {
			return
		}
	}
	t.Fatalf("%s:\n%s", phase, strings.Join(lines, "\n"))
}

func vimrc(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("testdata/vimrc.vim")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func scenario(t *testing.T, e *env, run func(phase string)) {
	run("make")
	run("install")
	e.commit(t, "foo.vim", map[string]string{
		"plugin/foo.vim": "let g:foo_version = 2\n",
		"doc/foo.txt":    "*foo.txt*\n\n*foo*\n*foo-new*\n",
	})
	run("update")
	run("updated")
}

func TestVim(t *testing.T) {
	vim := editor(t, "GODA_VIM", "vim")
	e := setup(t)
	scenario(t, e, func(phase string) {
		e.run(t, phase, vim, "-N", "-u", vimrc(t), "-U", "NONE", "-i", "NONE", "-n", "-es")
	})
}

func TestNeovim(t *testing.T) {
	nvim := editor(t, "GODA_NVIM", "nvim")
	e := setup(t)
	scenario(t, e, func(phase string) {
		e.run(t, phase, nvim, "--headless", "-u", vimrc(t), "-i", "NONE", "-n")
	})
}
