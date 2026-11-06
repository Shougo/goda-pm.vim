package installer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Shougo/goda-pm.vim/pm"
)

// Rollback is the revision of a plugin in a rollback file.
type Rollback struct {
	Name       string     `json:"name"`
	UpdateDate *time.Time `json:"updateDate,omitempty"`
	NewRev     string     `json:"newRev"`
	OldRev     string     `json:"oldRev,omitempty"`
	NewRevDate *time.Time `json:"newRevDate"`
	OldRevDate *time.Time `json:"oldRevDate,omitempty"`
}

// Rollbacks are the revisions of the plugins, by name: those of
// {base}/{name}/rollbacks/{date}/rollback.json.
type Rollbacks map[string]Rollback

// CheckHistory is the last check of a plugin (checkNotUpdated).
type CheckHistory struct {
	Name       string     `json:"name"`
	CheckDate  time.Time  `json:"checkDate"`
	NewRev     string     `json:"newRev"`
	OldRev     string     `json:"oldRev,omitempty"`
	NewRevDate *time.Time `json:"newRevDate"`
	OldRevDate *time.Time `json:"oldRevDate,omitempty"`
	Histories  []string   `json:"histories"`
}

// CheckHistories are the last checks, by name.
type CheckHistories map[string]CheckHistory

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// FormattedDate is the name of a rollback directory: yyMMddHHmmss.
func FormattedDate(t time.Time) string { return t.Format("060102150405") }

func (r *run) stateDir() string { return filepath.Join(r.a.BasePath, r.a.Name) }

func (r *run) loadRollbacks(date string) Rollbacks {
	if date == "" {
		return Rollbacks{}
	}
	file := filepath.Join(r.stateDir(), "rollbacks", date, "rollback.json")
	b, err := os.ReadFile(file)
	if err != nil {
		return Rollbacks{}
	}
	var rb Rollbacks
	if err := json.Unmarshal(b, &rb); err != nil {
		r.printError("Failed to parse rollback file: " + file + ": " + err.Error())
		return Rollbacks{}
	}
	return rb
}

func (r *run) saveRollbackFile(ups []*updated) {
	plugins, err := r.plugins(nil)
	if err != nil {
		r.printError(err.Error())
		return
	}
	rollbacks := Rollbacks{}
	r.parallel(plugins, func(p pm.Plugin, _ int) {
		pr, pa, ok := r.a.Protocols.Of(p)
		if !ok {
			return
		}
		rev, _ := pr.Revision(r.ctx, pa)
		date, _ := pr.DateFromRevision(r.ctx, pa, rev)
		r.e.mu.Lock()
		rollbacks[p.Name()] = Rollback{Name: p.Name(), NewRev: rev, NewRevDate: timePtr(date)}
		r.e.mu.Unlock()
	})
	now := time.Now()
	for _, u := range ups {
		rollbacks[u.plugin.Name()] = Rollback{Name: u.plugin.Name(), UpdateDate: &now, NewRev: u.newRev,
			OldRev: u.oldRev, NewRevDate: timePtr(u.newRevDate), OldRevDate: timePtr(u.oldRevDate)}
	}
	b, err := json.Marshal(rollbacks)
	if err != nil {
		r.printError(err.Error())
		return
	}
	for _, date := range []string{"latest", FormattedDate(now)} {
		dir := filepath.Join(r.stateDir(), "rollbacks", date)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			r.printError(err.Error())
			return
		}
		if err := os.WriteFile(filepath.Join(dir, "rollback.json"), b, 0o644); err != nil {
			r.printError(err.Error())
		}
	}
}

func (r *run) loadCheckHistories() CheckHistories {
	file := filepath.Join(r.stateDir(), "check_history.json")
	b, err := os.ReadFile(file)
	if err != nil {
		return CheckHistories{}
	}
	var ch CheckHistories
	if err := json.Unmarshal(b, &ch); err != nil {
		r.printError("Failed to parse check history file: " + file + ": " + err.Error())
		return CheckHistories{}
	}
	return ch
}

func (r *run) saveCheckHistories(checked []*updated) {
	ch := CheckHistories{}
	now := time.Now()
	for _, u := range checked {
		ch[u.plugin.Name()] = CheckHistory{Name: u.plugin.Name(), CheckDate: now, NewRev: u.newRev, OldRev: u.oldRev,
			NewRevDate: timePtr(u.newRevDate), OldRevDate: timePtr(u.oldRevDate), Histories: u.histories}
	}
	b, err := json.Marshal(ch)
	if err != nil {
		r.printError(err.Error())
		return
	}
	if err := os.MkdirAll(r.stateDir(), 0o755); err != nil {
		r.printError(err.Error())
		return
	}
	if err := os.WriteFile(filepath.Join(r.stateDir(), "check_history.json"), b, 0o644); err != nil {
		r.printError(err.Error())
	}
}

// fileMatch is a link found in a README.
type fileMatch struct {
	url          string
	line, column int
}

// checkInstalledFiles are the links to the checkExts in the README of the
// plugin.
func checkInstalledFiles(params Params, p pm.Plugin) []fileMatch {
	path := p.String("path")
	if path == "" {
		return nil
	}
	var content string
	for _, f := range []string{"README", "README.md", "README.rst", "README.txt"} {
		full := filepath.Join(path, f)
		if st, err := os.Stat(full); err == nil && st.Mode().IsRegular() {
			b, err := os.ReadFile(full)
			if err != nil {
				return nil
			}
			content = string(b)
			break
		}
	}
	if content == "" {
		return nil
	}
	re := (&run{params: params}).linkRegexp()
	if re == nil {
		return nil
	}
	var out []fileMatch
	for _, loc := range re.FindAllStringIndex(content, -1) {
		before := content[:loc[0]]
		line := strings.Count(before, "\n") + 1
		column := len(before) - strings.LastIndex(before, "\n")
		out = append(out, fileMatch{url: content[loc[0]:loc[1]], line: line, column: column})
	}
	return out
}
