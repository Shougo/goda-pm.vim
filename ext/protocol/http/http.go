// Package http is the protocol "http" of goda-pm.vim, as dpp-protocol-http:
// the plugins of a file or an archive (zip, tar.gz, ...) on the web, pinned
// by the SHA-1 of what is downloaded (the key "rev"):
//
//	[[plugins]]
//	repo = 'https://github.com/vim-fall/fall.vim/archive/refs/tags/v0.24.1.zip'
//	rev = '...'  # its SHA-1
//
// The files are downloaded and extracted by goda-pm itself: no curl, unzip
// nor tar is needed.
package http

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/Shougo/goda-pm.vim/pm"
)

// Protocol is the protocol "http".
type Protocol struct {
	pm.BaseProtocol
	// Client downloads the files (http.DefaultClient when nil).
	Client *http.Client

	mu    sync.Mutex
	cache map[string][]byte
}

// New returns the protocol, to add to the builder.
func New() *Protocol { return &Protocol{} }

func (*Protocol) Name() string { return "http" }

func (h *Protocol) client() *http.Client {
	if h.Client != nil {
		return h.Client
	}
	return http.DefaultClient
}

func (h *Protocol) Detect(_ context.Context, a pm.ProtocolArgs) (pm.Plugin, error) {
	if a.Plugin.String("rev") == "" {
		return nil, nil
	}
	u := NormalizeURL(a.Plugin.String("repo"), "")
	if u == "" {
		return nil, nil
	}
	dir := DirectoryName(u)
	return pm.Plugin{"path": a.BasePath + "/repos/" + dir, "name": path.Base(dir), "url": u}, nil
}

func (h *Protocol) URL(_ context.Context, a pm.ProtocolArgs) (string, error) {
	return NormalizeURL(a.Plugin.String("repo"), ""), nil
}

// download is the content of u: it is downloaded once for an install or an
// update (Revision, then SyncCommands).
func (h *Protocol) download(ctx context.Context, u string) ([]byte, error) {
	h.mu.Lock()
	if b, ok := h.cache[u]; ok {
		h.mu.Unlock()
		return b, nil
	}
	h.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("failed to fetch: %s: %s", u, resp.Status)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	if h.cache == nil {
		h.cache = map[string][]byte{}
	}
	h.cache[u] = b
	h.mu.Unlock()
	return b, nil
}

func (h *Protocol) forget(u string) {
	h.mu.Lock()
	delete(h.cache, u)
	h.mu.Unlock()
}

// Revision is the SHA-1 of what the URL has.
func (h *Protocol) Revision(ctx context.Context, a pm.ProtocolArgs) (string, error) {
	u := NormalizeURL(a.Plugin.String("repo"), "")
	if u == "" {
		return "unknown", nil
	}
	b, err := h.download(ctx, u)
	if err != nil {
		return "unknown", nil
	}
	sum := sha1.Sum(b)
	return hex.EncodeToString(sum[:]), nil
}

func (h *Protocol) RemoteRevision(ctx context.Context, a pm.ProtocolArgs) (string, error) {
	return h.Revision(ctx, a)
}

func (h *Protocol) SyncCommands(ctx context.Context, a pm.ProtocolArgs) ([]pm.Command, error) {
	repo := a.Plugin.String("repo")
	dest := a.Plugin.String("path")
	if repo == "" || dest == "" {
		return nil, nil
	}
	u := NormalizeURL(repo, "")
	// Downloaded again: what the URL has now.
	h.forget(u)
	rev, _ := h.Revision(ctx, a)
	if rev != a.Plugin.String("rev") {
		h.forget(u)
		return []pm.Command{{Func: func(context.Context, func(string)) error {
			return fmt.Errorf("%s: Revision check failed.\n"+
				"  Downloaded revision(SHA1): %s\n"+
				"  Specified revision(SHA1):  %s\n"+
				"  URL: %s", a.Plugin.Name(), rev, a.Plugin.String("rev"), u)
		}}}, nil
	}
	return []pm.Command{{Func: func(ctx context.Context, out func(string)) error {
		b, err := h.download(ctx, u)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return err
		}
		if !LooksLikeArchive(u) {
			return os.WriteFile(filepath.Join(dest, path.Base(mustURL(u).Path)), b, 0o644)
		}
		return Extract(b, mustURL(u).Path, dest)
	}}}, nil
}

func mustURL(u string) *url.URL {
	p, err := url.Parse(u)
	if err != nil {
		return &url.URL{}
	}
	return p
}

var archiveExts = []string{".tar.bz2", ".tar.gz", ".tar.xz", ".tgz", ".bz2", ".gz", ".tar", ".zip"}

// NormalizeURL is the URL of what repo is (a GitHub repository is the zip
// of its branch rev, "main" by default), or "" when it is not one of http.
func NormalizeURL(repo, rev string) string {
	raw := strings.TrimSpace(repo)
	if strings.HasPrefix(strings.ToLower(raw), "git+") {
		raw = raw[4:]
	}
	lower := strings.ToLower(raw)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	u.Host = strings.ToLower(u.Host)
	u.User = nil
	host := u.Hostname()
	segs := segments(u.Path)
	branch := rev
	if branch == "" {
		branch = "main"
	}
	switch {
	case host == "raw.githubusercontent.com":
	case slices.Contains(segs, "archive"):
		if rev != "" {
			return normalizeArchivePath(u, segs, branch)
		}
	case strings.Contains(host, "github.com") && slices.Contains(segs, "releases") && slices.Contains(segs, "download"):
	case strings.Contains(host, "bitbucket.org") && slices.Contains(segs, "get"):
	case strings.Contains(host, "github.com") && slices.Contains(segs, "raw"):
	case strings.Contains(host, "github.com") && len(segs) == 2:
		return "https://" + host + "/" + segs[0] + "/" + stripGitSuffix(segs[1]) +
			"/archive/refs/heads/" + url.PathEscape(branch) + ".zip"
	}
	return u.String()
}

func normalizeArchivePath(u *url.URL, segs []string, branch string) string {
	i := slices.Index(segs, "archive")
	var out []string
	if i+1 < len(segs) && segs[i+1] == "refs" {
		out = slices.Clone(segs[:len(segs)-1])
	} else {
		out = slices.Clone(segs[:i+1])
	}
	for j, s := range out {
		out[j] = url.PathEscape(s)
	}
	out = append(out, url.PathEscape(branch)+".zip")
	q := ""
	if u.RawQuery != "" {
		q = "?" + u.RawQuery
	}
	return u.Scheme + "://" + u.Host + "/" + strings.Join(out, "/") + q
}

func segments(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func stripGitSuffix(name string) string {
	if strings.HasSuffix(strings.ToLower(name), ".git") {
		return name[:len(name)-4]
	}
	return name
}

func removeExt(name string) string {
	lower := strings.ToLower(name)
	for _, e := range archiveExts {
		if strings.HasSuffix(lower, e) {
			return name[:len(name)-len(e)]
		}
	}
	if i := strings.LastIndex(name, "."); i > 0 {
		return name[:i]
	}
	return name
}

var hexRef = regexp.MustCompile(`(?i)-[0-9a-f]{7,40}$`)

// DirectoryName is where the plugin of the URL goes, under repos:
// "github.com/user/name" for the archives of a repository, the name of the
// file otherwise.
func DirectoryName(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		parts := segments(raw)
		if len(parts) == 0 {
			return raw
		}
		last := strings.SplitN(strings.SplitN(parts[len(parts)-1], "?", 2)[0], "#", 2)[0]
		if i := strings.LastIndex(last, "."); i >= 0 {
			last = last[:i]
		}
		return hexRef.ReplaceAllString(last, "")
	}
	host := strings.ToLower(u.Hostname())
	segs := segments(u.Path)
	if host == "raw.githubusercontent.com" && len(segs) > 0 {
		return removeExt(segs[len(segs)-1])
	}
	if len(segs) >= 2 {
		archive := slices.Contains(segs, "archive") ||
			(slices.Contains(segs, "releases") && slices.Contains(segs, "download")) ||
			slices.Contains(segs, "get")
		known := strings.Contains(host, "github.com") || strings.Contains(host, "gitlab.com") ||
			strings.Contains(host, "bitbucket.org") || strings.Contains(host, "git")
		if archive || known {
			return host + "/" + segs[0] + "/" + stripGitSuffix(segs[1])
		}
	}
	if len(segs) > 0 {
		return hexRef.ReplaceAllString(removeExt(segs[len(segs)-1]), "")
	}
	return host
}

// LooksLikeArchive is whether the URL is of an archive.
func LooksLikeArchive(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if archiveKind(u.Path) != "" {
		return true
	}
	if strings.ToLower(u.Hostname()) == "raw.githubusercontent.com" {
		return false
	}
	segs := segments(u.Path)
	return slices.Contains(segs, "archive") ||
		(slices.Contains(segs, "releases") && slices.Contains(segs, "download")) ||
		slices.Contains(segs, "get")
}

func archiveKind(p string) string {
	lower := strings.ToLower(p)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return "zip"
	case strings.HasSuffix(lower, ".tar"), strings.HasSuffix(lower, ".tar.gz"),
		strings.HasSuffix(lower, ".tgz"), strings.HasSuffix(lower, ".tar.bz2"):
		return "tar"
	}
	return ""
}

// Extract extracts the archive data (of the file name) into dest: the files
// of its only top directory when it has one, as the archives of GitHub.
func Extract(data []byte, name, dest string) error {
	files := map[string][]byte{}
	modes := map[string]os.FileMode{}
	switch {
	case archiveKind(name) == "tar":
		var r io.Reader = bytes.NewReader(data)
		lower := strings.ToLower(name)
		switch {
		case strings.HasSuffix(lower, ".gz"), strings.HasSuffix(lower, ".tgz"):
			gz, err := gzip.NewReader(r)
			if err != nil {
				return err
			}
			r = gz
		case strings.HasSuffix(lower, ".bz2"):
			r = bzip2.NewReader(r)
		}
		tr := tar.NewReader(r)
		for {
			hd, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if hd.Typeflag != tar.TypeReg {
				continue
			}
			b, err := io.ReadAll(tr)
			if err != nil {
				return err
			}
			files[hd.Name] = b
			modes[hd.Name] = hd.FileInfo().Mode().Perm()
		}
	default:
		// zip, and the archives of a URL without an extension.
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return fmt.Errorf("%s: not a zip archive: %w", name, err)
		}
		for _, f := range zr.File {
			if f.FileInfo().IsDir() {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return err
			}
			b, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return err
			}
			files[f.Name] = b
			modes[f.Name] = f.Mode().Perm()
		}
	}
	// The only top directory is stripped.
	prefix := ""
	for n := range files {
		top, _, ok := strings.Cut(n, "/")
		if !ok {
			prefix = ""
			break
		}
		if prefix == "" {
			prefix = top + "/"
		} else if prefix != top+"/" {
			prefix = ""
			break
		}
	}
	root, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	for n, b := range files {
		rel := filepath.FromSlash(strings.TrimPrefix(n, prefix))
		target := filepath.Join(root, rel)
		if target != root && !strings.HasPrefix(target, root+string(filepath.Separator)) {
			return fmt.Errorf("%s: a file outside of the directory: %s", name, n)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		mode := modes[n]
		if mode == 0 {
			mode = 0o644
		}
		if err := os.WriteFile(target, b, mode|0o200); err != nil {
			return err
		}
	}
	return nil
}
