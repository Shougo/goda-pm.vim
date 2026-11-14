package pm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Shougo/goda.vim/sdk"
)

// testHost records the functions called.
type testHost struct{ calls []string }

func (h *testHost) Meta() sdk.Meta { return sdk.Meta{} }
func (h *testHost) Call(_ context.Context, fn string, _ ...any) (any, error) {
	h.calls = append(h.calls, fn)
	return nil, nil
}
func (h *testHost) Notify(_ context.Context, fn string, _ ...any) error {
	h.calls = append(h.calls, fn)
	return nil
}
func (h *testHost) Eval(context.Context, string) (any, error) { return nil, nil }
func (h *testHost) Cmd(context.Context, string) error         { return nil }

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// The links that cannot be made (a hard link across drives on MS-Windows):
// copies instead.
func TestMergePluginsCopies(t *testing.T) {
	old := linkFile
	t.Cleanup(func() { linkFile = old })
	linkFile = func(bool, string, string) error { return errors.New("cross-device link") }

	base := t.TempDir()
	plugin := filepath.Join(base, "repos", "foo")
	writeFiles(t, plugin, map[string]string{
		"doc/foo.txt":          "*foo.txt*\n",
		"ftdetect/foo.vim":     "\" foo\n",
		"autoload/foo/bar.vim": "\" bar\n",
	})
	rtp := filepath.Join(base, "state", ".goda_pm")
	h := &testHost{}
	plugins := []Plugin{{"name": "foo", "path": plugin, "rtp": plugin, "merged": true}}
	s := &Server{}
	if err := s.mergePlugins(context.Background(), h, base, "state", rtp, `^tags$`, plugins, true); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"doc/foo.txt", "ftdetect/foo.vim", "autoload/foo/bar.vim"} {
		if _, err := os.Stat(filepath.Join(rtp, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(base, "state", "doc.json")); err != nil {
		t.Errorf("doc.json: %v", err)
	}
	for _, c := range h.calls {
		if c == "goda_pm#util#_error" {
			t.Errorf("an error was said: %v", h.calls)
		}
	}
}

// What can be neither linked nor copied is said, and the documents are
// merged again the next time (no doc.json).
func TestMergePluginsFails(t *testing.T) {
	oldLink, oldCopy := linkFile, copyFile
	t.Cleanup(func() { linkFile, copyFile = oldLink, oldCopy })
	linkFile = func(bool, string, string) error { return errors.New("no link") }
	copyFile = func(string, string, os.FileMode) error { return errors.New("no copy") }

	base := t.TempDir()
	plugin := filepath.Join(base, "repos", "foo")
	writeFiles(t, plugin, map[string]string{"doc/foo.txt": "*foo.txt*\n"})
	rtp := filepath.Join(base, "state", ".goda_pm")
	h := &testHost{}
	plugins := []Plugin{{"name": "foo", "path": plugin, "rtp": plugin}}
	s := &Server{}
	if err := s.mergePlugins(context.Background(), h, base, "state", rtp, `^tags$`, plugins, false); err != nil {
		t.Fatal(err)
	}
	said := false
	for _, c := range h.calls {
		said = said || c == "goda_pm#util#_error"
	}
	if !said {
		t.Errorf("not said: %v", h.calls)
	}
	if _, err := os.Stat(filepath.Join(base, "state", "doc.json")); err == nil {
		t.Error("doc.json was written")
	}
	// The next time, they can be: they are merged (doc.json was not kept).
	linkFile, copyFile = oldLink, oldCopy
	h = &testHost{}
	if err := s.mergePlugins(context.Background(), h, base, "state", rtp, `^tags$`, plugins, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(rtp, "doc", "foo.txt")); err != nil {
		t.Errorf("not merged again: %v", err)
	}
}
