// Package installer is the ext "installer" of goda-pm.vim, as
// dpp-ext-installer: it installs, updates and builds the plugins.
//
//	:call goda_pm#async_ext_action('installer', 'install')
//	:call goda_pm#async_ext_action('installer', 'update', #{names: ['goda-fi.vim']})
//	:call goda_pm#async_ext_action('installer', 'checkNotUpdated')
package installer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Shougo/goda-pm.vim/pm"
	"github.com/Shougo/goda.vim/sdk"
)

// Params are the ext params (extParams.installer), as dpp-ext-installer's.
type Params struct {
	// CheckDiff shows the changes of the documents of the updated plugins.
	CheckDiff bool `json:"checkDiff"`
	// CheckExts are the extensions of the links (in the README and the
	// documents) that are reported: a direct link to a .zip added to a
	// README may be a takeover of the account.
	CheckExts []string `json:"checkExts"`
	// LogFilePath is a file the messages are appended to.
	LogFilePath string `json:"logFilePath"`
	// MaxInactiveDays: an update after this many days without any is
	// reported.
	MaxInactiveDays int `json:"maxInactiveDays"`
	// MaxProcesses is the number of plugins processed at once.
	MaxProcesses int `json:"maxProcesses"`
	// MinCommitDays: a commit newer than this many days is not taken.
	MinCommitDays int `json:"minCommitDays"`
	// Wait is the wait after each plugin (milliseconds).
	Wait int `json:"wait"`
}

// DefaultParams are the default params.
func DefaultParams() Params {
	return Params{
		CheckExts:       []string{"zip"},
		MaxInactiveDays: 180,
		MaxProcesses:    5,
	}
}

// Attrs are the attributes of a plugin (its extAttrs), as
// dpp-ext-installer's.
type Attrs struct {
	// InstallerBuild is the build command, run by the shell in the
	// directory of the plugin after an update.
	InstallerBuild string `json:"installerBuild"`
	// InstallerFrozen: the plugin is not updated.
	InstallerFrozen          bool `json:"installerFrozen"`
	InstallerMaxInactiveDays *int `json:"installerMaxInactiveDays"`
	InstallerMinCommitDays   *int `json:"installerMinCommitDays"`
}

// InstallParams are the action params of install, reinstall and update.
type InstallParams struct {
	// Names are the plugins (all when empty).
	Names []string `json:"names"`
	// Rollback is the date of the rollback file ("latest" for the last
	// one) the plugins go back to.
	Rollback string `json:"rollback"`
}

// CheckParams are the action params of checkNotUpdated.
type CheckParams struct {
	// Force updates without asking.
	Force bool     `json:"force"`
	Names []string `json:"names"`
}

// Ext is the ext "installer".
type Ext struct {
	busy sync.Mutex

	mu         sync.Mutex
	failed     []pm.Plugin
	logs       []string
	updateLogs []string
	updated    []pm.Plugin
	logFileMu  sync.Mutex
}

// New returns the ext, to add to the builder.
func New() *Ext { return &Ext{} }

func (*Ext) Name() string { return "installer" }

// run is the context of an action.
type run struct {
	e       *Ext
	a       pm.ActionArgs
	params  Params
	ctx     context.Context
	logFile string

	// The editor (vim.go).
	vimOnce     sync.Once
	isNvim      bool
	isHeadless  bool
	progressMu  sync.Mutex
	progressWin int64
}

func (e *Ext) newRun(ctx context.Context, a pm.ActionArgs) (*run, error) {
	p := DefaultParams()
	if err := pm.DecodeParams(a.ExtParams, &p); err != nil {
		return nil, err
	}
	if p.MaxProcesses < 1 {
		p.MaxProcesses = 1
	}
	r := &run{e: e, a: a, params: p, ctx: ctx}
	if p.LogFilePath != "" {
		if f, err := pm.Expand(ctx, a.Host, p.LogFilePath); err == nil {
			r.logFile = f
		}
	}
	return r, nil
}

func (e *Ext) action(desc string, f func(r *run) (any, error)) pm.Action {
	return pm.Action{Description: desc, Run: func(ctx context.Context, a pm.ActionArgs) (any, error) {
		r, err := e.newRun(ctx, a)
		if err != nil {
			return nil, err
		}
		return f(r)
	}}
}

// exclusive runs f unless another update runs.
func (e *Ext) exclusive(f func(r *run) (any, error)) func(r *run) (any, error) {
	return func(r *run) (any, error) {
		if !e.busy.TryLock() {
			return nil, errors.New("goda-pm: the installer is busy: wait for it")
		}
		defer e.busy.Unlock()
		return f(r)
	}
}

func (e *Ext) Actions() map[string]pm.Action {
	return map[string]pm.Action{
		"build": e.action("Build plugins", e.exclusive(func(r *run) (any, error) {
			var ip InstallParams
			if err := pm.DecodeParams(r.a.ActionParams, &ip); err != nil {
				return nil, err
			}
			plugins, err := r.plugins(ip.Names)
			if err != nil {
				return nil, err
			}
			r.parallel(plugins, func(p pm.Plugin, _ int) { r.build(p) })
			return nil, nil
		})),
		"checkNotUpdated": e.action("Check not updated plugins", e.exclusive(func(r *run) (any, error) {
			var cp CheckParams
			if err := pm.DecodeParams(r.a.ActionParams, &cp); err != nil {
				return nil, err
			}
			plugins, err := r.plugins(cp.Names)
			if err != nil {
				return nil, err
			}
			checked := r.checkRemotePlugins(notFrozen(plugins))
			return nil, r.promptAndUpdate(checked, cp)
		})),
		"denoCache": e.action("Execute deno cache for plugins", func(r *run) (any, error) {
			var ip InstallParams
			if err := pm.DecodeParams(r.a.ActionParams, &ip); err != nil {
				return nil, err
			}
			plugins, err := r.plugins(ip.Names)
			if err != nil {
				return nil, err
			}
			r.denoCache(plugins)
			return nil, nil
		}),
		"getFailed": e.action("Get failed plugins", func(*run) (any, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			return list(e.failed), nil
		}),
		"getLogs": e.action("Get logs", func(*run) (any, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			return list(e.logs), nil
		}),
		"getNotInstalled": e.action("Get not installed plugins", func(r *run) (any, error) {
			var ip InstallParams
			if err := pm.DecodeParams(r.a.ActionParams, &ip); err != nil {
				return nil, err
			}
			plugins, err := r.plugins(ip.Names)
			if err != nil {
				return nil, err
			}
			return list(notInstalled(plugins)), nil
		}),
		"getNotUpdated": e.action("Get not updated plugins", e.exclusive(func(r *run) (any, error) {
			var ip InstallParams
			if err := pm.DecodeParams(r.a.ActionParams, &ip); err != nil {
				return nil, err
			}
			plugins, err := r.plugins(ip.Names)
			if err != nil {
				return nil, err
			}
			var out []pm.Plugin
			for _, c := range r.checkRemotePlugins(notFrozen(plugins)) {
				out = append(out, c.plugin)
			}
			return list(out), nil
		})),
		"getUpdateLogs": e.action("Get update logs", func(*run) (any, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			return list(e.updateLogs), nil
		}),
		"getUpdated": e.action("Get updated plugins", func(*run) (any, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			return list(e.updated), nil
		}),
		"install": e.action("Install plugins", e.exclusive(func(r *run) (any, error) {
			var ip InstallParams
			if err := pm.DecodeParams(r.a.ActionParams, &ip); err != nil {
				return nil, err
			}
			plugins, err := r.plugins(ip.Names)
			if err != nil {
				return nil, err
			}
			rollbacks := r.loadRollbacks(ip.Rollback)
			return nil, r.updatePlugins(notInstalled(plugins), rollbacks, len(ip.Names) == 0)
		})),
		"reinstall": e.action("Reinstall plugins", e.exclusive(func(r *run) (any, error) {
			var ip InstallParams
			if err := pm.DecodeParams(r.a.ActionParams, &ip); err != nil {
				return nil, err
			}
			if len(ip.Names) == 0 {
				r.printError("names must be set for reinstall plugins.")
				return nil, nil
			}
			plugins, err := r.plugins(ip.Names)
			if err != nil {
				return nil, err
			}
			rollbacks := r.loadRollbacks(ip.Rollback)
			for _, p := range plugins {
				if path := p.String("path"); isDir(path) {
					if err := os.RemoveAll(path); err != nil {
						r.printError(fmt.Sprintf("Failed to remove plugin directory: %s: %v", path, err))
					}
				}
			}
			return nil, r.updatePlugins(plugins, rollbacks, false)
		})),
		"update": e.action("Update plugins", e.exclusive(func(r *run) (any, error) {
			var ip InstallParams
			if err := pm.DecodeParams(r.a.ActionParams, &ip); err != nil {
				return nil, err
			}
			plugins, err := r.plugins(ip.Names)
			if err != nil {
				return nil, err
			}
			rollbacks := r.loadRollbacks(ip.Rollback)
			return nil, r.updatePlugins(notFrozen(plugins), rollbacks, len(ip.Names) == 0)
		})),
	}
}

// list is s, or an empty list instead of nil (an empty list in Vim).
func list[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return slices.Clone(s)
}

func attrs(p pm.Plugin) Attrs {
	var at Attrs
	if m, ok := p["extAttrs"].(map[string]any); ok {
		_ = pm.DecodeParams(m, &at)
	}
	return at
}

func notFrozen(plugins []pm.Plugin) []pm.Plugin {
	var out []pm.Plugin
	for _, p := range plugins {
		if !attrs(p).InstallerFrozen {
			out = append(out, p)
		}
	}
	return out
}

func notInstalled(plugins []pm.Plugin) []pm.Plugin {
	var out []pm.Plugin
	for _, p := range plugins {
		if path := p.String("path"); path != "" && !isDir(path) {
			out = append(out, p)
		}
	}
	return out
}

func isDir(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// plugins are those of the state named names (all when empty).
func (r *run) plugins(names []string) ([]pm.Plugin, error) {
	v, err := r.a.Host.Call(r.ctx, "goda_pm#util#_get_plugins")
	if err != nil {
		return nil, err
	}
	var all []map[string]any
	if err := sdk.Decode(v, &all); err != nil {
		return nil, err
	}
	var out []pm.Plugin
	for _, m := range all {
		p := pm.Plugin(m)
		if len(names) == 0 || slices.Contains(names, p.Name()) {
			out = append(out, p)
		}
	}
	return out, nil
}

// parallel runs f for the plugins, MaxProcesses at once.
func (r *run) parallel(plugins []pm.Plugin, f func(p pm.Plugin, index int)) {
	sem := make(chan struct{}, r.params.MaxProcesses)
	var wg sync.WaitGroup
	for i, p := range plugins {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			f(p, i+1)
			if r.params.Wait > 0 {
				time.Sleep(time.Duration(r.params.Wait) * time.Millisecond)
			}
		}()
	}
	wg.Wait()
}

func (r *run) log(msg string, update bool) {
	r.e.mu.Lock()
	r.e.logs = append(r.e.logs, msg)
	if update {
		r.e.updateLogs = append(r.e.updateLogs, msg)
	}
	r.e.mu.Unlock()
	if r.logFile == "" {
		return
	}
	r.e.logFileMu.Lock()
	defer r.e.logFileMu.Unlock()
	f, err := os.OpenFile(r.logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(msg + "\n")
}

func (r *run) printError(msg string) {
	if strings.Contains(msg, "fatal: could not read Username for ") {
		_, _ = r.a.Host.Call(r.ctx, "goda_pm#util#_error",
			"Target repository name is invalid.\nYou may have used the wrong plugin name.")
	}
	_, _ = r.a.Host.Call(r.ctx, "goda_pm#util#_error", msg)
	r.log(msg, true)
}

func (r *run) doautocmd(event string) {
	_ = r.a.Host.Cmd(r.ctx, "if exists('#User#"+event+"') | doautocmd <nomodeline> User "+event+" | endif")
}

// runCommand runs the command in the directory of the plugin: its standard
// output goes to onStdout, its error output is shown as errors.
func (r *run) runCommand(p pm.Plugin, c pm.Command, onStdout func(string)) (bool, int) {
	if c.Func != nil {
		if err := c.Func(r.ctx, onStdout); err != nil {
			r.printError(err.Error())
			return false, 1
		}
		return true, 0
	}
	cmd := exec.CommandContext(r.ctx, c.Command, c.Args...)
	if path := p.String("path"); isDir(path) {
		cmd.Dir = path
	}
	return r.exec(cmd, onStdout)
}

func (r *run) exec(cmd *exec.Cmd, onStdout func(string)) (bool, int) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		r.printError(err.Error())
		return false, -1
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		r.printError(err.Error())
		return false, -1
	}
	if err := cmd.Start(); err != nil {
		r.printError(err.Error())
		return false, -1
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); lines(stdout, onStdout) }()
	go func() { defer wg.Done(); lines(stderr, r.printError) }()
	wg.Wait()
	err = cmd.Wait()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return false, ee.ExitCode()
		}
		r.printError(err.Error())
		return false, -1
	}
	return true, 0
}

func lines(rd io.Reader, f func(string)) {
	sc := bufio.NewScanner(rd)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		f(strings.TrimRight(sc.Text(), "\r"))
	}
}

// updated is an updated plugin.
type updated struct {
	plugin       pm.Plugin
	protocol     pm.Protocol
	pargs        pm.ProtocolArgs
	oldRev       string
	newRev       string
	oldRevDate   time.Time
	newRevDate   time.Time
	url          string
	logMessage   string
	changesCount int
	histories    []string
}

func (r *run) updatePlugins(plugins []pm.Plugin, rollbacks Rollbacks, allPlugins bool) error {
	r.e.mu.Lock()
	r.e.failed, r.e.logs, r.e.updated, r.e.updateLogs = nil, nil, nil, nil
	r.e.mu.Unlock()
	if len(plugins) == 0 {
		r.noTargetPlugins()
		return nil
	}
	r.printMessage("Start: " + time.Now().Format(time.RFC1123))
	latest := r.loadRollbacks("latest")
	histories := r.loadCheckHistories()
	var mu sync.Mutex
	var ups []*updated
	var failed []pm.Plugin
	r.parallel(plugins, func(p pm.Plugin, index int) {
		u, ok := r.updatePlugin(p, index, len(plugins), latest, rollbacks, histories, allPlugins)
		mu.Lock()
		defer mu.Unlock()
		if !ok {
			failed = append(failed, p)
		} else if u != nil {
			ups = append(ups, u)
		}
	})
	return r.finalizeUpdate(ups, failed)
}

func (r *run) noTargetPlugins() {
	r.printError("Target plugins are not found.")
	r.printError("You may have used the wrong plugin name, or all of the plugins are already installed.")
	r.doautocmd("GodaPm:ext:installer:updateDone")
}

// updatePlugin installs or updates the plugin: the updated plugin (nil when
// it is not), and whether it did not fail.
func (r *run) updatePlugin(p pm.Plugin, index, max int, latest, rollbacks Rollbacks,
	histories CheckHistories, allPlugins bool) (*updated, bool) {
	r.printProgress(fmt.Sprintf("[%d/%d] %s", index, max, p.Name()))
	if p.Bool("local") {
		r.printMessage(fmt.Sprintf("%q is local plugin.  The update is skipped.", p.Name()))
		return nil, true
	}
	pr, pa, ok := r.a.Protocols.Of(p)
	if !ok {
		return nil, true
	}
	oldRev, _ := pr.Revision(r.ctx, pa)
	if rev := p.String("rev"); rev != "" {
		q := p.Clone()
		q["rev"] = ""
		r.revisionLock(pr, r.a.Protocols.Args(p.String("protocol"), q))
	}
	commands, err := pr.SyncCommands(r.ctx, pa)
	if err != nil {
		r.printError(fmt.Sprintf("%s: %v", p.Name(), err))
		return nil, false
	}
	if rb, ok := rollbacks[p.Name()]; ok {
		rc, err := pr.RollbackCommands(r.ctx, pa, rb.NewRev)
		if err != nil {
			r.printError(fmt.Sprintf("%s: %v", p.Name(), err))
			return nil, false
		}
		commands = append(commands, rc...)
	}
	if p.Has("hook_pre_update") {
		r.callHook("pre_update", p)
	}
	success := len(commands) != 0
	for _, c := range commands {
		if ok, code := r.runCommand(p, c, r.printProgress); !ok {
			r.printError(fmt.Sprintf("Command failed with exit code %d: %s", code, commandName(c)))
			success = false
			break
		}
	}
	if p.String("rev") != "" {
		r.revisionLock(pr, pa)
	}
	if !success {
		return nil, false
	}
	newRev, _ := pr.Revision(r.ctx, pa)
	if newRev != "" {
		if rev := p.String("rev"); rev != "" && rev != newRev {
			r.printMessage(fmt.Sprintf("%s: revision is changed.\n  Current commit:  %s", p.Name(), newRev))
		}
		if !allPlugins && p.String("rev") == "" && newRev == oldRev {
			r.printMessage(fmt.Sprintf("%s: revision is unchanged.\n  Current commit:  %s", p.Name(), newRev))
		}
	}
	if oldRev != "" && newRev == oldRev {
		return nil, true
	}
	logMessage := r.logMessage(p, pr, pa, oldRev, newRev)
	changes := r.changesCount(p, pr, pa, oldRev, newRev)
	if p.Has("hook_post_update") {
		r.callHook("post_update", p)
	}
	if !r.build(p) {
		return nil, false
	}
	if matches := checkInstalledFiles(r.params, p); len(matches) > 0 {
		var details []string
		for _, m := range matches[:min(5, len(matches))] {
			details = append(details, fmt.Sprintf("%d:%d %s", m.line, m.column, m.url))
		}
		r.printError(fmt.Sprintf("%s: installed files check failed.\n%s", p.Name(), strings.Join(details, "\n")))
	}
	url, _ := pr.URL(r.ctx, pa)
	oldDate, _ := pr.DateFromRevision(r.ctx, pa, oldRev)
	newDate, _ := pr.DateFromRevision(r.ctx, pa, newRev)
	r.checkPluginCommits(pr, pa, latest, histories, p, oldRev, newRev, oldDate, newDate)
	return &updated{plugin: p, protocol: pr, pargs: pa, oldRev: oldRev, newRev: newRev,
		oldRevDate: oldDate, newRevDate: newDate, url: url, logMessage: logMessage, changesCount: changes}, true
}

func commandName(c pm.Command) string {
	if c.Func != nil {
		return "(internal)"
	}
	return c.Command
}

func (r *run) revisionLock(pr pm.Protocol, pa pm.ProtocolArgs) {
	if !isDir(pa.Plugin.String("path")) || pa.Plugin.String("rev") == "" {
		return
	}
	cmds, err := pr.RevisionLockCommands(r.ctx, pa)
	if err != nil {
		r.printError(err.Error())
		return
	}
	for _, c := range cmds {
		r.runCommand(pa.Plugin, c, r.printProgress)
	}
}

func (r *run) finalizeUpdate(ups []*updated, failed []pm.Plugin) error {
	slices.SortFunc(ups, func(a, b *updated) int { return strings.Compare(a.plugin.Name(), b.plugin.Name()) })
	calledDepends := map[string]bool{}
	callMakeState := false
	for _, u := range ups {
		if u.plugin.Has("hook_done_update") {
			r.callHook("done_update", u.plugin)
		}
		deps, err := r.plugins(u.plugin.Strings("depends"))
		if err == nil && len(u.plugin.Strings("depends")) > 0 {
			for _, d := range deps {
				if d.Has("hook_depends_update") && !calledDepends[d.Name()] {
					calledDepends[d.Name()] = true
					r.callHook("depends_update", d)
				}
			}
		}
		if r.checkDiff(u) || u.plugin.Bool("merged") {
			callMakeState = true
		}
	}
	if len(ups) > 0 {
		r.printUpdated(ups)
		r.saveRollbackFile(ups)
	}
	if len(failed) > 0 {
		var names []string
		for _, p := range failed {
			names = append(names, p.Name())
		}
		r.printMessage("Failed plugins:\n" + strings.Join(names, "\n") + "\n" +
			"Please read the error message log with the :message command.")
	}
	r.e.mu.Lock()
	r.e.updated = nil
	for _, u := range ups {
		r.e.updated = append(r.e.updated, u.plugin)
	}
	r.e.failed = failed
	r.e.mu.Unlock()
	r.closeProgressWindow()
	var err error
	if callMakeState && r.a.MakeState != nil {
		err = r.a.MakeState(r.ctx)
	}
	_ = r.a.Host.Cmd(r.ctx, "redraw")
	r.printMessage("Done: " + time.Now().Format(time.RFC1123))
	r.doautocmd("GodaPm:ext:installer:updateDone")
	return err
}

func (r *run) logMessage(p pm.Plugin, pr pm.Protocol, pa pm.ProtocolArgs, oldRev, newRev string) string {
	if newRev == oldRev || newRev == "" || oldRev == "" {
		return ""
	}
	cmds, err := pr.LogCommands(r.ctx, pa, oldRev, newRev)
	if err != nil {
		return ""
	}
	var out []string
	for _, c := range cmds {
		r.runCommand(p, c, func(l string) { out = append(out, l) })
	}
	return strings.Join(out, "\n")
}

func (r *run) changesCount(p pm.Plugin, pr pm.Protocol, pa pm.ProtocolArgs, oldRev, newRev string) int {
	if newRev == oldRev || newRev == "" || oldRev == "" {
		return 0
	}
	cmds, err := pr.ChangesCountCommands(r.ctx, pa, oldRev, newRev)
	if err != nil {
		return 0
	}
	n := 0
	for _, c := range cmds {
		r.runCommand(p, c, func(l string) { n, _ = strconv.Atoi(strings.TrimSpace(l)) })
	}
	return n
}

// build runs the installerBuild of the plugin.
func (r *run) build(p pm.Plugin) bool {
	b := attrs(p).InstallerBuild
	path := p.String("path")
	if b == "" || !isDir(path) {
		return true
	}
	v, err := r.a.Host.Eval(r.ctx, "[&shell, &shellcmdflag]")
	if err != nil {
		r.printError(err.Error())
		return false
	}
	var sh []string
	if err := sdk.Decode(v, &sh); err != nil || len(sh) != 2 {
		r.printError("goda-pm: cannot get 'shell'")
		return false
	}
	args := append(strings.Fields(sh[1]), b)
	cmd := exec.CommandContext(r.ctx, sh[0], args...)
	cmd.Dir = path
	ok, _ := r.exec(cmd, r.printProgress)
	return ok
}

func (r *run) denoCache(plugins []pm.Plugin) {
	if _, err := exec.LookPath("deno"); err != nil {
		return
	}
	r.parallel(plugins, func(p pm.Plugin, _ int) {
		path := p.String("path")
		if path == "" || !isDir(filepath.Join(path, "denops")) || p.Name() == "denops.vim" {
			return
		}
		cmd := exec.CommandContext(r.ctx, "deno", "cache", "--no-check", "--reload", ".")
		cmd.Dir = path
		cmd.Env = append(os.Environ(), "NO_COLOR=1")
		r.exec(cmd, r.printProgress)
	})
}

func (r *run) linkRegexp() *regexp.Regexp {
	var exts []string
	for _, e := range r.params.CheckExts {
		if e = strings.TrimPrefix(e, "."); e != "" {
			exts = append(exts, regexp.QuoteMeta(e))
		}
	}
	if len(exts) == 0 {
		return nil
	}
	return regexp.MustCompile(`(?i)https?://[^\s"]+\.(` + strings.Join(exts, "|") + `)(\b|\?|#|")`)
}

// checkDiff shows the changes of the documents: whether there are.
func (r *run) checkDiff(u *updated) bool {
	if u.newRev == u.oldRev || u.newRev == "" || u.oldRev == "" {
		return false
	}
	cmds, err := u.protocol.DiffCommands(r.ctx, u.pargs, u.oldRev, u.newRev)
	if err != nil {
		return false
	}
	changed := len(cmds) == 0
	re := r.linkRegexp()
	for _, c := range cmds {
		var output []string
		r.runCommand(u.plugin, c, func(l string) {
			if l != "" {
				output = append(output, l)
			}
		})
		if re != nil {
			for _, l := range output {
				if !strings.HasPrefix(l, "+") {
					continue
				}
				if m := re.FindStringSubmatch(l); m != nil {
					_, _ = r.a.Host.Call(r.ctx, "goda_pm#util#_error", fmt.Sprintf(
						"%s: A new direct link to a .%s file has been added to the README.\n"+
							"This could indicate a potential account takeover or suspicious distribution. Please review carefully.",
						u.plugin.Name(), m[1]))
				}
			}
		}
		if r.params.CheckDiff && len(output) > 0 {
			r.outputDiff(output)
		}
		if len(output) > 0 {
			changed = true
		}
	}
	return changed
}

var breaking = regexp.MustCompile(`.*!.*:|BREAKING CHANGE:`)

func (r *run) printUpdated(ups []*updated) {
	var lines []string
	var breakings []string
	for _, u := range ups {
		lines = append(lines, formatPlugin(u, time.Now()))
		if breaking.MatchString(u.logMessage) {
			breakings = append(breakings, "  "+u.plugin.Name())
		}
	}
	r.printMessage("Updated plugins:\n" + strings.Join(lines, "\n"))
	if len(breakings) > 0 {
		r.printMessage("Breaking updated plugins:\n" + strings.Join(breakings, "\n"))
	}
}

var supportedHost = regexp.MustCompile(`^https?://(github\.com|codeberg\.org)/`)

func formatDate(t time.Time) string { return t.Local().Format("2006-01-02 15:04:05") }

// formatPlugin is the line of an updated plugin.
func formatPlugin(u *updated, now time.Time) string {
	compare := ""
	if u.oldRev != "" && supportedHost.MatchString(u.url) {
		base := regexp.MustCompile(`^\w+:`).ReplaceAllString(strings.TrimSuffix(u.url, ".git"), "https:")
		compare = fmt.Sprintf("\n    %s/compare/%s...%s", base, u.oldRev, u.newRev)
	}
	changes := ""
	if u.changesCount == 1 {
		changes = "(1 change)"
	} else if u.changesCount > 1 {
		changes = fmt.Sprintf("(%d changes)", u.changesCount)
	}
	date := ""
	if !u.oldRevDate.IsZero() || !u.newRevDate.IsZero() {
		oldDate, newDate, ago := "-", "-", ""
		if !u.oldRevDate.IsZero() {
			oldDate = formatDate(u.oldRevDate)
		}
		if !u.newRevDate.IsZero() {
			newDate = formatDate(u.newRevDate)
			ago = " (" + TimeAgo(u.newRevDate, now) + ")"
		}
		date = fmt.Sprintf("\n    %s -> %s%s", oldDate, newDate, ago)
	}
	return "  " + u.plugin.Name() + changes + compare + date
}

// TimeAgo is how long ago t was: "3 days ago".
func TimeAgo(t, now time.Time) string {
	sec := int(now.Sub(t).Seconds())
	unit := func(n int, s string) string {
		if n == 1 {
			return fmt.Sprintf("%d %s ago", n, s)
		}
		return fmt.Sprintf("%d %ss ago", n, s)
	}
	switch {
	case sec < 0:
		return "just now"
	case sec < 60:
		return unit(sec, "second")
	case sec < 3600:
		return unit(sec/60, "minute")
	case sec < 86400:
		return unit(sec/3600, "hour")
	case sec/86400 < 30:
		return unit(sec/86400, "day")
	case sec/86400/30 < 12:
		return unit(sec/86400/30, "month")
	}
	return unit(sec/86400/365, "year")
}

// dateDiffDays is the number of whole days between a and b.
func dateDiffDays(a, b time.Time) int {
	d := a.Sub(b)
	if d < 0 {
		d = -d
	}
	return int(d / (24 * time.Hour))
}

func (r *run) printErrorLines(lines ...string) {
	_, _ = r.a.Host.Call(r.ctx, "goda_pm#util#_error", strings.Join(lines, "\n"))
}

// checkPluginCommits checks the dates of the commits of an update: whether
// it is refused (a commit newer than minCommitDays).
func (r *run) checkPluginCommits(pr pm.Protocol, pa pm.ProtocolArgs, latest Rollbacks, histories CheckHistories,
	p pm.Plugin, oldRev, newRev string, oldDate, newDate time.Time) bool {
	if oldDate.IsZero() || newDate.IsZero() {
		return false
	}
	if r.checkCommitAge(p, oldDate, newDate) {
		return true
	}
	r.checkRollbackAndHistoryAge(pr, pa, latest, histories, p, oldRev, newRev, oldDate, newDate)
	return false
}

func (r *run) checkCommitAge(p pm.Plugin, oldDate, newDate time.Time) bool {
	at := attrs(p)
	minDays := r.params.MinCommitDays
	if at.InstallerMinCommitDays != nil {
		minDays = *at.InstallerMinCommitDays
	}
	maxDays := r.params.MaxInactiveDays
	if at.InstallerMaxInactiveDays != nil {
		maxDays = *at.InstallerMaxInactiveDays
	}
	now := time.Now()
	diff := dateDiffDays(now, newDate)
	if diff < minDays {
		r.printErrorLines(
			p.Name()+": update is invalid!",
			"  Current day:     "+formatDate(now),
			"  Current commit:  "+formatDate(oldDate),
			"  New commit:      "+formatDate(newDate),
			fmt.Sprintf("  Days since last commit: %d (minimum required: %d)", diff, minDays),
		)
		return true
	}
	if diff > maxDays {
		r.printErrorLines(
			p.Name()+": inactive update is detected!",
			"  Current day:     "+formatDate(now),
			"  Current commit:  "+formatDate(oldDate),
			"  New commit:      "+formatDate(newDate),
			fmt.Sprintf("  Days since last commit: %d", diff),
			"This plugin had no updates for a long period before this.",
			"You should check the commit.",
		)
	}
	return false
}

func (r *run) checkRollbackAndHistoryAge(pr pm.Protocol, pa pm.ProtocolArgs, latest Rollbacks, histories CheckHistories,
	p pm.Plugin, oldRev, newRev string, oldDate, newDate time.Time) {
	rb, hasRb := latest[p.Name()]
	// (The dates of the commits are in seconds.)
	if hasRb && rb.UpdateDate != nil && rb.UpdateDate.Truncate(time.Second).After(newDate) {
		r.printErrorLines(
			p.Name()+": older commit is detected!",
			"  The last update: "+formatDate(*rb.UpdateDate),
			"  Current commit:  "+formatDate(oldDate),
			"  New commit:      "+formatDate(newDate),
			"You should check the commit.",
		)
	}
	ch, hasCh := histories[p.Name()]
	if hasCh && ch.NewRevDate != nil && ch.NewRevDate.After(newDate) {
		r.printErrorLines(
			p.Name()+": older commit is detected!",
			"  The last check: "+formatDate(ch.CheckDate),
			"  Current commit: "+formatDate(oldDate),
			"  Checked commit: "+formatDate(*ch.NewRevDate),
			"  New commit:     "+formatDate(newDate),
			"You should check the commit.",
		)
	}
	if !hasCh || len(ch.Histories) == 0 {
		return
	}
	prev := map[string]bool{}
	for _, h := range ch.Histories {
		prev[h] = true
	}
	hs, _ := pr.Histories(r.ctx, pa, newRev, oldRev)
	checkDate := ch.CheckDate
	if hasRb && rb.UpdateDate != nil {
		checkDate = *rb.UpdateDate
	}
	for _, h := range hs {
		if prev[h] {
			continue
		}
		d, _ := pr.DateFromRevision(r.ctx, pa, h)
		if !d.IsZero() && d.Before(checkDate) {
			r.printErrorLines(
				p.Name()+": suspicious old commit is detected in history!",
				"  Commit: "+h,
				"  Commit date: "+formatDate(d),
				"  Last update/check: "+formatDate(checkDate),
				"You should check the commit.",
			)
		}
	}
}

// checkRemotePlugins are the plugins that an update would change.
func (r *run) checkRemotePlugins(plugins []pm.Plugin) []*updated {
	if len(plugins) == 0 {
		r.noTargetPlugins()
		return nil
	}
	r.printMessage("Start: " + time.Now().Format(time.RFC1123))
	latest := r.loadRollbacks("latest")
	histories := r.loadCheckHistories()
	var mu sync.Mutex
	var ups, checked []*updated
	r.parallel(plugins, func(p pm.Plugin, index int) {
		u, ok := r.checkRemotePlugin(p, index, len(plugins), latest, histories)
		if u == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		checked = append(checked, u)
		if ok {
			ups = append(ups, u)
		}
	})
	slices.SortFunc(ups, func(a, b *updated) int { return strings.Compare(a.plugin.Name(), b.plugin.Name()) })
	if len(ups) > 0 {
		r.printUpdated(ups)
	}
	r.saveCheckHistories(checked)
	r.closeProgressWindow()
	_ = r.a.Host.Cmd(r.ctx, "redraw")
	r.printMessage("Done: " + time.Now().Format(time.RFC1123))
	return ups
}

// checkRemotePlugin is the plugin checked (nil when it is not changed), and
// whether it is to be updated.
func (r *run) checkRemotePlugin(p pm.Plugin, index, max int, latest Rollbacks, histories CheckHistories) (*updated, bool) {
	r.printProgress(fmt.Sprintf("[%d/%d] %s", index, max, p.Name()))
	if p.Bool("local") {
		return nil, false
	}
	pr, pa, ok := r.a.Protocols.Of(p)
	if !ok || !isDir(p.String("path")) {
		return nil, false
	}
	cmds, err := pr.CheckRemoteCommands(r.ctx, pa)
	if err != nil {
		r.printError(err.Error())
		return nil, false
	}
	var logMessage []string
	for _, c := range cmds {
		if ok, code := r.runCommand(p, c, func(l string) { logMessage = append(logMessage, l) }); !ok {
			r.printError(fmt.Sprintf("Command failed with exit code %d: %s", code, commandName(c)))
			return nil, false
		}
	}
	if len(logMessage) == 0 {
		return nil, false
	}
	url, _ := pr.URL(r.ctx, pa)
	oldRev, _ := pr.Revision(r.ctx, pa)
	newRev, _ := pr.RemoteRevision(r.ctx, pa)
	if newRev == oldRev {
		return nil, false
	}
	oldDate, _ := pr.DateFromRevision(r.ctx, pa, oldRev)
	newDate, _ := pr.DateFromRevision(r.ctx, pa, newRev)
	hs, _ := pr.Histories(r.ctx, pa, newRev, oldRev)
	u := &updated{plugin: p, protocol: pr, pargs: pa, oldRev: oldRev, newRev: newRev,
		oldRevDate: oldDate, newRevDate: newDate, url: url,
		logMessage: strings.Join(logMessage, "\n"), changesCount: len(logMessage), histories: hs}
	if p.Has("hook_post_check_update") {
		r.callHook("post_check_update", p, map[string]any{"plugin": p, "oldRev": oldRev, "newRev": newRev})
		if v, err := r.a.Host.Eval(r.ctx, "g:goda_pm#hook_result"); err == nil && pm.Truthy(v) {
			return u, false
		}
	}
	if r.checkPluginCommits(pr, pa, latest, histories, p, oldRev, newRev, oldDate, newDate) {
		return u, false
	}
	return u, true
}

func (r *run) promptAndUpdate(checked []*updated, cp CheckParams) error {
	names := map[string]bool{}
	all, err := r.plugins(cp.Names)
	if err != nil {
		return err
	}
	ni := notInstalled(all)
	if len(ni) > 0 {
		var lines []string
		for _, p := range ni {
			lines = append(lines, "  "+p.Name())
		}
		r.printMessage("Not installed plugins:\n" + strings.Join(lines, "\n"))
	}
	for _, u := range checked {
		names[u.plugin.Name()] = true
	}
	for _, p := range ni {
		names[p.Name()] = true
	}
	if len(names) == 0 {
		r.printMessage("Updated plugins are not found.")
		r.doautocmd("GodaPm:ext:installer:updateDone")
		return nil
	}
	if !cp.Force {
		v, err := r.a.Host.Call(r.ctx, "confirm", "Update now?", "yes\nNo", 2)
		if err != nil || !isOne(v) {
			r.doautocmd("GodaPm:ext:installer:updateDone")
			return err
		}
	}
	var targets []string
	for n := range names {
		targets = append(targets, n)
	}
	slices.Sort(targets)
	plugins, err := r.plugins(targets)
	if err != nil {
		return err
	}
	return r.updatePlugins(plugins, Rollbacks{}, false)
}

func isOne(v any) bool {
	switch n := v.(type) {
	case int64:
		return n == 1
	case float64:
		return n == 1
	case int:
		return n == 1
	}
	return false
}
