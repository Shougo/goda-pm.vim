package http

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryName(t *testing.T) {
	tests := []struct{ url, want string }{
		{"https://github.com/Shougo/dpp-protocol-git/archive/refs/heads/main.zip", "github.com/Shougo/dpp-protocol-git"},
		{"https://github.com/vim-fall/fall.vim/archive/refs/tags/v0.24.1.zip", "github.com/vim-fall/fall.vim"},
		{"https://bitbucket.org/spilt/vim-peg/get/c6be9c909538.zip", "bitbucket.org/spilt/vim-peg"},
		{"https://raw.githubusercontent.com/Shougo/shougo-s-github/master/vim/colors/candy.vim", "candy"},
		{"https://github.com/owner/repo/raw/branch/path/to/file.txt", "github.com/owner/repo"},
		{"https://gitlab.com/foo/bar/-/archive/main/bar-main.zip", "gitlab.com/foo/bar"},
		{"https://example.com/downloads/mylib-abcdef1234567.zip", "mylib"},
		{"https://example.com/downloads/foo.zip", "foo"},
		{"some/path/to/file.ext", "file"},
	}
	for _, tt := range tests {
		if got := DirectoryName(tt.url); got != tt.want {
			t.Errorf("DirectoryName(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestNormalizeURL(t *testing.T) {
	tests := []struct{ repo, rev, want string }{
		{"https://github.com/owner/repo", "", "https://github.com/owner/repo/archive/refs/heads/main.zip"},
		{"https://github.com/owner/repo", "dev-branch", "https://github.com/owner/repo/archive/refs/heads/dev-branch.zip"},
		{"https://github.com/owner/repo/archive/refs/heads/main.zip", "feature/xyz",
			"https://github.com/owner/repo/archive/refs/heads/feature%2Fxyz.zip"},
		{"https://github.com/owner/repo/archive/main.zip", "v2", "https://github.com/owner/repo/archive/v2.zip"},
		{"http://example.com/path/to/file.zip", "", "http://example.com/path/to/file.zip"},
		{"git+https://github.com/owner/repo/releases/download/v1.0.0/asset.tar.gz", "",
			"https://github.com/owner/repo/releases/download/v1.0.0/asset.tar.gz"},
		{"git@github.com:owner/repo.git", "", ""},
		{"ftp://example.com/repo.zip", "", ""},
		{"owner/repo", "", ""},
		{"https://user:pass@example.com/path/to/res.zip", "", "https://example.com/path/to/res.zip"},
		{"HTTPs://Example.COM/a/archive/asset.ZIP", "", "https://example.com/a/archive/asset.ZIP"},
		{"https://gitlab.com/foo/bar/-/archive/main/bar-main.zip", "", "https://gitlab.com/foo/bar/-/archive/main/bar-main.zip"},
	}
	for _, tt := range tests {
		if got := NormalizeURL(tt.repo, tt.rev); got != tt.want {
			t.Errorf("NormalizeURL(%q, %q) = %q, want %q", tt.repo, tt.rev, got, tt.want)
		}
	}
}

func TestExtract(t *testing.T) {
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	for _, n := range []string{"repo-main/plugin/a.vim", "repo-main/doc/a.txt"} {
		w, _ := zw.Create(n)
		w.Write([]byte(n))
	}
	zw.Close()
	dir := t.TempDir()
	if err := Extract(zb.Bytes(), "/x/main.zip", dir); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "plugin", "a.vim")); err != nil || string(b) != "repo-main/plugin/a.vim" {
		t.Errorf("zip: %q %v", b, err)
	}

	var tb bytes.Buffer
	gz := gzip.NewWriter(&tb)
	tw := tar.NewWriter(gz)
	for _, n := range []string{"plugin/b.vim", "README"} {
		tw.WriteHeader(&tar.Header{Name: n, Mode: 0o644, Size: int64(len(n)), Typeflag: tar.TypeReg})
		tw.Write([]byte(n))
	}
	tw.Close()
	gz.Close()
	dir = t.TempDir()
	if err := Extract(tb.Bytes(), "/x/a.tar.gz", dir); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "plugin", "b.vim")); err != nil || string(b) != "plugin/b.vim" {
		t.Errorf("tar: %q %v", b, err)
	}

	zb.Reset()
	zw = zip.NewWriter(&zb)
	for _, n := range []string{"a/x", "a/../../evil"} {
		w, _ := zw.Create(n)
		w.Write([]byte("x"))
	}
	zw.Close()
	if err := Extract(zb.Bytes(), "a.zip", t.TempDir()); err == nil {
		t.Error("a file outside of the directory is extracted")
	}
}
