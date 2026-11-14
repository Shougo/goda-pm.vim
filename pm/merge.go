package pm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/Shougo/goda.vim/sdk"
)

// mergePlugins links the ftdetect and doc files of the plugins, and the
// files of the merged plugins, into the runtime directory of the state:
// dpp.vim's mergePlugins().
func (s *Server) mergePlugins(ctx context.Context, h sdk.Host, basePath, name, rtpDir, skipPattern string,
	plugins []Plugin, windows bool) error {
	skip, err := regexp.Compile(skipPattern)
	if err != nil {
		return err
	}
	ftdetectDir := rtpDir + "/ftdetect"
	docDir := rtpDir + "/doc"
	for _, d := range []string{ftdetectDir, docDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	recordDocs := getRecordDocs(skip, plugins)
	mergeDocs := !sameDocs(loadRecordDocs(basePath, name), recordDocs)
	if mergeDocs {
		entries, err := os.ReadDir(docDir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := os.RemoveAll(filepath.Join(docDir, e.Name())); err != nil {
				return err
			}
		}
	}
	srcs := []string{"ftdetect"}
	if mergeDocs {
		srcs = []string{"doc", "ftdetect"}
	}
	// What could not be placed: said once, at the end (dpp.vim throws).
	var failed []error
	docsFailed := false
	for _, p := range plugins {
		path := p.String("path")
		for _, src := range srcs {
			srcDir := path + "/" + src
			if path == "" || !isDir(srcDir) {
				continue
			}
			entries, err := os.ReadDir(srcDir)
			if err != nil {
				continue
			}
			dest := ftdetectDir
			if src == "doc" {
				dest = docDir
			}
			for _, e := range entries {
				if skip.MatchString(e.Name()) {
					continue
				}
				if errs := linkPath(windows, filepath.Join(srcDir, e.Name()), filepath.Join(dest, e.Name())); len(errs) > 0 {
					failed = append(failed, errs...)
					docsFailed = docsFailed || src == "doc"
				}
			}
		}
	}
	if mergeDocs {
		if hasHelpFiles(docDir) {
			if _, err := h.Call(ctx, "goda_pm#util#_helptags", docDir); err != nil {
				s.printError(ctx, h, ":helptags failed: %v", err)
			}
		}
		tagsPath := docDir + "/tags"
		var tagLines []string
		if b, err := os.ReadFile(tagsPath); err == nil {
			for _, l := range strings.Split(string(b), "\n") {
				if l != "" {
					tagLines = append(tagLines, l)
				}
			}
		}
		tagLines = append(tagLines, generateTaglines(plugins)...)
		slices.Sort(tagLines)
		tagLines = slices.Compact(tagLines)
		if err := os.WriteFile(tagsPath, []byte(strings.Join(tagLines, "\n")), 0o644); err != nil {
			return err
		}
		// Not when a document is missing: they are merged again next time
		// (the same doc.json would say there is nothing to do).
		docJSON := filepath.Join(basePath, name, "doc.json")
		if docsFailed {
			_ = os.Remove(docJSON)
		} else {
			b, _ := json.Marshal(recordDocs)
			if err := os.WriteFile(docJSON, b, 0o644); err != nil {
				return err
			}
		}
	}

	defaultSkip := map[string]bool{
		".git": true, "deno.json": true, "deno.jsonc": true, "doc": true,
		"ftdetect": true, "import_map.json": true, "import_map.jsonc": true,
	}
	for _, p := range plugins {
		path := p.String("path")
		if !p.Bool("merged") || !isDir(path) {
			continue
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if defaultSkip[e.Name()] || skip.MatchString(e.Name()) {
				continue
			}
			failed = append(failed, linkPath(windows, filepath.Join(path, e.Name()), filepath.Join(rtpDir, e.Name()))...)
		}
	}
	if len(failed) > 0 {
		s.printError(ctx, h, "%d files could not be merged into %s: %v", len(failed), rtpDir, failed[0])
	}
	return nil
}

// linkPath links src (a file, or the files of a directory, recursively) to
// dest: a symbolic link, or a hard link on MS-Windows; a copy when it
// cannot be made (a hard link across drives, a symbolic link where they
// are not allowed). What exists is kept. The errors are those of what
// could not be placed.
func linkPath(windows bool, src, dest string) []error {
	st, err := os.Stat(src)
	if err != nil {
		return nil
	}
	if st.IsDir() {
		if _, err := os.Lstat(dest); err != nil {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return []error{err}
			}
		}
		if !isDir(dest) {
			return nil
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return []error{err}
		}
		var errs []error
		for _, e := range entries {
			errs = append(errs, linkPath(windows, filepath.Join(src, e.Name()), filepath.Join(dest, e.Name()))...)
		}
		return errs
	}
	if _, err := os.Lstat(dest); err == nil {
		return nil
	}
	err = linkFile(windows, src, dest)
	if err == nil {
		return nil
	}
	if cerr := copyFile(src, dest, st.Mode()); cerr != nil {
		return []error{fmt.Errorf("%w; copy: %v", err, cerr)}
	}
	return nil
}

// linkFile makes the link of linkPath (a variable for the tests).
var linkFile = func(windows bool, src, dest string) error {
	if windows {
		return os.Link(src, dest)
	}
	return os.Symlink(src, dest)
}

// copyFile is the copy of linkPath (a variable for the tests).
var copyFile = func(src, dest string, mode os.FileMode) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dest, b, mode.Perm())
}

func hasHelpFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if strings.HasSuffix(e.Name(), ".txt") || strings.HasSuffix(e.Name(), ".jax") {
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
				return true
			}
		}
		if isDir(p) && hasHelpFiles(p) {
			return true
		}
	}
	return false
}

// getRecordDocs are the sizes of the documents of the plugins: they are
// merged again when they change.
func getRecordDocs(skip *regexp.Regexp, plugins []Plugin) map[string]int64 {
	docs := map[string]int64{}
	for _, p := range plugins {
		for _, f := range []string{"README.md", "README.mkd"} {
			full := p.String("rtp") + "/" + f
			if st, err := os.Stat(full); err == nil {
				docs[full] = st.Size()
			}
		}
		path := p.String("path")
		srcDir := path + "/doc"
		if path == "" || !isDir(srcDir) {
			continue
		}
		entries, err := os.ReadDir(srcDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if skip.MatchString(e.Name()) {
				continue
			}
			full := filepath.Join(srcDir, e.Name())
			if st, err := os.Stat(full); err == nil {
				docs[full] = st.Size()
			}
		}
	}
	return docs
}

func loadRecordDocs(basePath, name string) map[string]int64 {
	b, err := os.ReadFile(filepath.Join(basePath, name, "doc.json"))
	if err != nil {
		return nil
	}
	var docs map[string]int64
	if json.Unmarshal(b, &docs) != nil {
		return nil
	}
	return docs
}

func sameDocs(a, b map[string]int64) bool {
	if a == nil {
		return false
	}
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

var markdownHeading = regexp.MustCompile(`^\s*(#+\s*(.+)\s*$|<h[1-6][^>]*>\s*(.+)\s*</h[1-6]>)`)
var spaces = regexp.MustCompile(`\s+`)

// markdownTag is a tag of a heading of a README.
type markdownTag struct {
	title   string
	pattern string
}

func detectTagsInMarkdown(content string) []markdownTag {
	var tags []markdownTag
	for _, line := range strings.Split(content, "\n") {
		m := markdownHeading.FindStringSubmatchIndex(line)
		if m == nil {
			continue
		}
		var title string
		if m[4] >= 0 {
			title = line[m[4]:m[5]]
		} else {
			title = line[m[6]:m[7]]
		}
		pattern := line[m[2]:m[3]]
		pattern = spaces.ReplaceAllString(pattern, `\s\+`)
		pattern = strings.ReplaceAll(pattern, "/", `\/`)
		pattern = strings.ReplaceAll(pattern, ".", `\.`)
		tags = append(tags, markdownTag{title: spaces.ReplaceAllString(title, "-"), pattern: "/" + pattern})
	}
	return tags
}

// generateTaglines are the tags of the headings of the READMEs of the
// plugins without documents.
func generateTaglines(plugins []Plugin) []string {
	var lines []string
	for _, p := range plugins {
		rtp := p.String("rtp")
		if isDir(rtp + "/doc") {
			continue
		}
		for _, f := range []string{"README.md", "README.mkd"} {
			full := rtp + "/" + f
			b, err := os.ReadFile(full)
			if err != nil {
				continue
			}
			for _, tag := range detectTagsInMarkdown(string(b)) {
				title := p.Name() + "-" + tag.title
				if strings.EqualFold(p.Name(), tag.title) {
					title = p.Name()
				}
				lines = append(lines, title+"\t"+full+"\t"+tag.pattern)
			}
		}
	}
	return lines
}
