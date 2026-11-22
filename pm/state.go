package pm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/Shougo/goda.vim/sdk"
)

// ConfigArgs are what the configuration works with.
type ConfigArgs struct {
	Host sdk.Host
	// BasePath is the base directory of the state, Name its name (that of
	// the editor by default: vim or nvim).
	BasePath string
	Name     string
	// ExtraArgs are those of goda_pm#make_state().
	ExtraArgs map[string]any
	// Options start as the default ones; the configuration changes them.
	Options *Options

	protocols *Protocols
}

// ConfigReturn is what the configuration gives: the plugins and what goes
// with them, as dpp.vim's ConfigReturn.
type ConfigReturn struct {
	// CheckFiles are files that goda_pm#check_files() checks are older
	// than the state, as the configuration's own files.
	CheckFiles []string
	// Ftplugins are the ftplugins (MergeFtplugins).
	Ftplugins map[string]string
	// Groups are the options of groups: a plugin of the group (its key
	// "group") has them under its own.
	Groups map[string]Plugin
	// HooksFiles are hooks files of the ftplugins.
	HooksFiles    []string
	MultipleHooks []MultipleHook
	Plugins       []Plugin
	// StateLines are put in the startup script (lazy.MakeState gives them).
	StateLines []string
}

// ConfigFunc is the configuration: the plugins of the state.
type ConfigFunc func(ctx context.Context, c *ConfigArgs) (*ConfigReturn, error)

// IsNvim is whether the editor is Neovim.
func (c *ConfigArgs) IsNvim() bool { return c.Host.Meta().Host == sdk.HostNvim }

// Has is has(feature) of the editor.
func (c *ConfigArgs) Has(ctx context.Context, feature string) (bool, error) {
	v, err := c.Host.Call(ctx, "has", feature)
	return Truthy(v), err
}

// Expand expands ~ and $VAR in path, as the editor does (the environment of
// the editor: $VAR set in the vimrc too).
func (c *ConfigArgs) Expand(ctx context.Context, path string) (string, error) {
	return Expand(ctx, c.Host, path)
}

// URL is the URL the protocol name gets the plugin from ("" without the
// protocol): with the options the configuration set so far.
func (c *ConfigArgs) URL(ctx context.Context, protocol string, p Plugin) (string, error) {
	pr, ok := c.protocols.Get(protocol)
	if !ok {
		return "", nil
	}
	return pr.URL(ctx, c.protocols.Args(protocol, p))
}

// Expand expands ~ and $VAR in path with the editor (goda_pm#util#_expand()).
func Expand(ctx context.Context, h sdk.Host, path string) (string, error) {
	v, err := h.Call(ctx, "goda_pm#util#_expand", path)
	if err != nil {
		return "", err
	}
	s, _ := v.(string)
	return s, nil
}

// vimState is what makeState needs of the editor.
type vimState struct {
	InitRuntimepath  string   `json:"init_rtp"`
	Rtps             []string `json:"rtps"`
	RuntimePath      string   `json:"runtime"`
	StateVersion     int      `json:"version"`
	DidLoadFiletypes bool     `json:"did_load_filetypes"`
	DidLoadFtplugin  bool     `json:"did_load_ftplugin"`
	Windows          bool     `json:"win"`
	Lua              bool     `json:"lua"`
	ConfigDir        string   `json:"config_dir"`
}

const vimStateExpr = `#{
	\ init_rtp: g:goda_pm._init_runtimepath,
	\ rtps: goda_pm#util#_split_rtp(g:goda_pm._init_runtimepath),
	\ runtime: goda_pm#util#_get_runtime_path(),
	\ version: g:goda_pm._state_version,
	\ did_load_filetypes: g:goda_pm._did_load_filetypes ? v:true : v:false,
	\ did_load_ftplugin: g:goda_pm._did_load_ftplugin ? v:true : v:false,
	\ win: has('win32') ? v:true : v:false,
	\ lua: has('nvim') || has('lua') ? v:true : v:false,
	\ config_dir: get(g:goda_pm.settings, 'config_dir', ''),
	\ }`

// stateArgs are the arguments of a makeState.
type stateArgs struct {
	BasePath  string         `json:"base_path"`
	Name      string         `json:"name"`
	ExtraArgs map[string]any `json:"extra_args"`
}

func isAbs(p string) bool {
	return filepath.IsAbs(p) || strings.HasPrefix(p, "/") ||
		(len(p) >= 3 && p[1] == ':' && (p[2] == '/' || p[2] == '\\'))
}

func isDir(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

var revSanitizer = regexp.MustCompile(`[^\w.-]`)
var continuation = regexp.MustCompile(`\n\s*\\`)

// InitPlugin completes a plugin of the configuration, as dpp.vim's
// initPlugin(): the full path (with its rev and script_type), rtp, lazy,
// merged and the hooks (the lua_* ones become hook_* ones when the editor
// has Lua).
func InitPlugin(p Plugin, basePath string, hasLua bool) Plugin {
	path := p.String("path")
	if path == "" {
		return p
	}
	p["sourced"] = false
	if !isAbs(path) {
		path = basePath + "/" + path
	}
	if rev := p.String("rev"); rev != "" {
		path += "_" + revSanitizer.ReplaceAllString(rev, "_")
	}
	if st := p.String("script_type"); st != "" {
		path += "/" + st
	}
	p["path"] = path
	rtp, hasRtp := p["rtp"].(string)
	if !hasRtp || rtp != "" {
		if rtp == "" {
			rtp = path
		} else {
			rtp = path + "/" + rtp
		}
	}
	p["rtp"] = strings.TrimSuffix(rtp, "/")
	if s, ok := p["depends"].(string); ok {
		p["depends"] = []any{s}
	}
	if !p.Has("lazy") {
		lazy := false
		for k := range p {
			if strings.HasPrefix(k, "on_") {
				lazy = true
				break
			}
		}
		p["lazy"] = lazy
	}
	if !p.Has("merged") {
		p["merged"] = !p.Bool("lazy") && !p.Has("local") && !p.Has("if") && !p.Has("hook_post_update")
	}
	hooks := map[string]string{}
	for k, v := range p {
		if s, ok := v.(string); ok && strings.HasPrefix(k, "hook_") {
			hooks[k] = continuation.ReplaceAllString(s, "")
		}
	}
	if hasLua {
		keys := slices.Sorted(func(yield func(string) bool) {
			for k := range p {
				if strings.HasPrefix(k, "lua_") && !yield(k) {
					return
				}
			}
		})
		for _, k := range keys {
			s, ok := p[k].(string)
			if !ok {
				continue
			}
			hook := "hook_" + strings.TrimPrefix(k, "lua_")
			lua := "lua <<EOF\n" + s + "\nEOF\n"
			if cur := hooks[hook]; cur != "" {
				hooks[hook] = cur + "\n" + lua
			} else {
				hooks[hook] = lua
			}
		}
	}
	for k, v := range hooks {
		p[k] = v
	}
	return p
}

// detectPlugin completes the plugin with the first protocol that gets it.
func detectPlugin(ctx context.Context, ps *Protocols, p Plugin) error {
	names := ps.Names()
	if name := p.String("protocol"); name != "" {
		if _, ok := ps.Get(name); !ok {
			return fmt.Errorf("not available protocol: %q in %s", name, p.Name())
		}
		names = []string{name}
	}
	for _, name := range names {
		pr, _ := ps.Get(name)
		d, err := pr.Detect(ctx, ps.Args(name, p))
		if err != nil {
			return fmt.Errorf("%s: protocol %s: %w", p.Name(), name, err)
		}
		if d == nil {
			continue
		}
		for k, v := range d {
			if !p.Has(k) {
				p[k] = v
			}
		}
		p["protocol"] = name
		if n := d.String("name"); n != "" {
			p["name"] = n
		}
		break
	}
	return nil
}

func readHooksFile(ctx context.Context, h sdk.Host, path string) ([]string, error) {
	full, err := Expand(ctx, h, path)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n"), nil
}

func (s *Server) printError(ctx context.Context, h sdk.Host, format string, args ...any) {
	_ = h.Notify(ctx, "goda_pm#util#_error", fmt.Sprintf(format, args...))
}

// makeState runs the configuration and writes the state: startup.vim,
// state.vim and the runtime directory of the merged plugins, as dpp.vim's
// makeState().
//
// The steps call the editor in the order dpp.vim does (the errors it prints
// keep their order): those of the plugins, the runtimepath, the startup
// script, then the files.
func (s *Server) makeState(ctx context.Context, h sdk.Host, sa stateArgs) error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	if sa.ExtraArgs == nil {
		sa.ExtraArgs = map[string]any{}
	}
	m, err := s.newStateMaker(ctx, h, sa)
	if err != nil {
		return err
	}
	ret, vs := m.ret, m.vs

	record, available := m.loadPlugins()
	if err := resetRtpDir(m.rtpDir); err != nil {
		return err
	}

	layout, err := layoutRuntimepath(vs.Rtps, vs.RuntimePath, m.rtpDir, available)
	if err != nil {
		return err
	}
	for _, e := range layout.errors {
		s.printError(ctx, h, "%s", e)
	}
	jv, err := h.Call(ctx, "goda_pm#util#_join_rtp", layout.rtps, vs.InitRuntimepath, m.rtpDir)
	if err != nil {
		return err
	}
	runtimepath, _ := jv.(string)

	startup := startupHead(vs.StateVersion, runtimepath, available.list)
	m.readFtpluginHooksFiles()
	checkFiles := configCheckFiles(ret, vs.ConfigDir)
	startup = append(startup, filetypeOff(vs)...)
	startup = append(startup, ret.StateLines...)
	lines, files, err := m.inlineVimrcs()
	if err != nil {
		return err
	}
	startup = append(startup, lines...)
	checkFiles = append(checkFiles, files...)
	adds, files := pluginHooks(available.list, ret.Ftplugins)
	startup = append(startup, adds...)
	checkFiles = append(checkFiles, files...)
	mhs := make([]MultipleHook, 0, len(ret.MultipleHooks))
	for _, mh := range ret.MultipleHooks {
		mhs = append(mhs, m.readMultipleHook(mh))
	}
	adds, sources, multipleHooks := splitMultipleHooks(mhs, available.byName, layout.nonLazy)
	startup = append(startup, adds...)
	startup = append(startup, layout.hookSources...)
	startup = append(startup, sources...)

	if err := writeFile(filepath.Join(m.stateDir, "startup.vim"), startup); err != nil {
		return err
	}
	existing, err := m.existingFiles(checkFiles)
	if err != nil {
		return err
	}
	inStartup := map[string]bool{}
	for _, p := range available.list {
		inStartup[p.Name()] = true
	}
	if err := writeStateVim(filepath.Join(m.stateDir, "state.vim"), []any{
		statePlugins(record, inStartup), map[string]any{}, m.options, existing, multipleHooks, sa.ExtraArgs,
	}); err != nil {
		return err
	}

	if err := s.mergePlugins(ctx, h, m.basePath, m.name, m.rtpDir, m.options.SkipMergeFilenamePattern, available.list, vs.Windows); err != nil {
		return err
	}
	if err := m.writeFtplugins(); err != nil {
		return err
	}
	return h.Cmd(ctx, "if exists('#User#GodaPm:makeStatePost') | doautocmd <nomodeline> User GodaPm:makeStatePost | endif")
}

// stateMaker is what the steps of makeState share: the state of the editor
// and the configuration.
type stateMaker struct {
	s   *Server
	ctx context.Context
	h   sdk.Host

	vs       vimState
	options  Options
	ps       *Protocols
	ret      *ConfigReturn
	basePath string
	name     string
	// stateDir has startup.vim and state.vim, rtpDir the merged plugins.
	stateDir string
	rtpDir   string
}

// newStateMaker gets the state of the editor and runs the configuration.
func (s *Server) newStateMaker(ctx context.Context, h sdk.Host, sa stateArgs) (*stateMaker, error) {
	m := &stateMaker{s: s, ctx: ctx, h: h, basePath: sa.BasePath, name: sa.Name}
	v, err := h.Eval(ctx, strings.ReplaceAll(vimStateExpr, "\n\t\\", ""))
	if err != nil {
		return nil, err
	}
	if err := sdk.Decode(v, &m.vs); err != nil {
		return nil, err
	}
	m.stateDir = filepath.Join(m.basePath, m.name)
	m.rtpDir = m.basePath + "/" + m.name + "/.goda_pm"

	m.options = DefaultOptions()
	m.ps = s.protocols(h, m.basePath, &m.options)
	c := &ConfigArgs{Host: h, BasePath: m.basePath, Name: m.name, ExtraArgs: sa.ExtraArgs,
		Options: &m.options, protocols: m.ps}
	if s.config == nil {
		return nil, errors.New("no configuration: call SetConfig() of the builder")
	}
	ret, err := s.config(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("configuration: %w", err)
	}
	if ret == nil {
		ret = &ConfigReturn{}
	}
	if ret.Ftplugins == nil {
		ret.Ftplugins = map[string]string{}
	}
	m.ret = ret
	return m, nil
}

func (m *stateMaker) printError(format string, args ...any) {
	m.s.printError(m.ctx, m.h, format, args...)
}

// readHooks reads and parses a hooks file. It prints the error, if any:
// that of reading after readPrefix, that of parsing after parsePrefix.
func (m *stateMaker) readHooks(path, readPrefix, parsePrefix string) (Plugin, bool) {
	lines, err := readHooksFile(m.ctx, m.h, path)
	if err != nil {
		m.printError("%s%v", readPrefix, err)
		return nil, false
	}
	parsed, err := ParseHooksFile(m.options.HooksFileMarker, lines, path)
	if err != nil {
		m.printError("%s%v", parsePrefix, err)
		return nil, false
	}
	return parsed, true
}

// availablePlugins are the plugins that are installed and whose "if" is
// true, in the order of the configuration.
type availablePlugins struct {
	list   []Plugin
	byName map[string]Plugin
}

func (a *availablePlugins) remove(name string) {
	if i := slices.IndexFunc(a.list, func(q Plugin) bool { return q.Name() == name }); i >= 0 {
		a.list = slices.Delete(a.list, i, i+1)
		delete(a.byName, name)
	}
}

// loadPlugins completes the plugins of the configuration: record has all
// of them (those of state.vim), available those to load.
func (m *stateMaker) loadPlugins() (map[string]Plugin, *availablePlugins) {
	record := map[string]Plugin{}
	available := &availablePlugins{byName: map[string]Plugin{}}
	for _, base := range m.ret.Plugins {
		p := m.loadPlugin(base)
		if p.Name() == "" {
			continue
		}
		if _, dup := record[p.Name()]; dup {
			m.printError("Duplicated plugin is detected: %q", p.Name())
			available.remove(p.Name())
		}
		record[p.Name()] = p
		if isDir(p.String("path")) {
			ok, err := m.checkIf(p)
			if err != nil {
				m.printError("%v", err)
			}
			if ok {
				available.list = append(available.list, p)
				available.byName[p.Name()] = p
			}
		}
	}
	return record, available
}

// loadPlugin completes a plugin of the configuration: its protocol, its
// groups, its hooks files, and InitPlugin.
func (m *stateMaker) loadPlugin(base Plugin) Plugin {
	p := base.Clone()
	if err := detectPlugin(m.ctx, m.ps, p); err != nil {
		m.printError("%v", err)
	}
	if m.ret.Groups != nil && p.Has("group") {
		for _, g := range p.Strings("group") {
			opts, ok := m.ret.Groups[g]
			if !ok {
				m.printError("Not available group: %q in %s", g, p.Name())
				continue
			}
			p = Merge(opts, p)
		}
	}
	if p.Has("hooks_file") {
		for _, hf := range p.Strings("hooks_file") {
			if parsed, ok := m.readHooks(hf, p.Name()+": hooks_file: ", p.Name()+": "); ok {
				p = Merge(p, parsed)
			}
		}
	}
	if p.String("path") == "" {
		m.printError("\"path\" must be set: %q", p.Name())
	}
	return InitPlugin(p, m.basePath, m.vs.Lua)
}

// checkIf is whether the "if" of the plugin is true: an expression of the
// editor or a value.
func (m *stateMaker) checkIf(p Plugin) (bool, error) {
	cond, ok := p["if"]
	if !ok {
		return true, nil
	}
	if expr, ok := cond.(string); ok {
		r, err := m.h.Call(m.ctx, "eval", expr)
		if err != nil {
			return false, fmt.Errorf("%s: if: %w", p.Name(), err)
		}
		return Truthy(r), nil
	}
	return Truthy(cond), nil
}

// resetRtpDir empties the runtime directory of the merged plugins but its
// doc directory, which mergePlugins updates itself (doc.json).
func resetRtpDir(rtpDir string) error {
	if !isDir(rtpDir) {
		return os.MkdirAll(rtpDir, 0o755)
	}
	entries, err := os.ReadDir(rtpDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() != "doc" {
			if err := os.RemoveAll(filepath.Join(rtpDir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// runtimeLayout is the runtimepath of the startup.
type runtimeLayout struct {
	// rtps are the directories, for goda_pm#util#_join_rtp().
	rtps []string
	// nonLazy are the names of the plugins loaded at startup but their
	// dependencies.
	nonLazy map[string]bool
	// hookSources are the hook_source of the plugins loaded at startup.
	hookSources []string
	// errors are the dependencies that are not available.
	errors []string
}

// layoutRuntimepath puts the plugins loaded at startup (not lazy, by
// priority, then their dependencies) before the runtime of Vim, and their
// after directories after it; rtpDir (the merged plugins) is the last before
// the runtime, rtpDir/after the last of all. It marks those plugins
// "sourced".
func layoutRuntimepath(rtps []string, runtimePath, rtpDir string, available *availablePlugins) (runtimeLayout, error) {
	l := runtimeLayout{rtps: slices.Clone(rtps), nonLazy: map[string]bool{}}
	runtimeIndex := slices.Index(l.rtps, runtimePath)
	if runtimeIndex < 0 {
		return l, errors.New("Vim runtime path is not found in runtimepath")
	}
	add := func(p Plugin) {
		rtp := p.String("rtp")
		if rtp == "" {
			return
		}
		if !p.Bool("merged") {
			l.rtps = slices.Insert(l.rtps, runtimeIndex, rtp)
			if after := rtp + "/after"; isDir(after) {
				l.rtps = slices.Insert(l.rtps, slices.Index(l.rtps, runtimePath)+1, after)
			}
		}
		p["sourced"] = true
	}
	var nonLazy []Plugin
	for _, p := range available.list {
		if p.String("rtp") != "" && !p.Bool("lazy") {
			nonLazy = append(nonLazy, p)
		}
	}
	slices.SortStableFunc(nonLazy, func(a, b Plugin) int { return b.Int("priority") - a.Int("priority") })
	var depends []string
	seen := map[string]bool{}
	for _, p := range nonLazy {
		l.nonLazy[p.Name()] = true
		for _, d := range p.Strings("depends") {
			if _, ok := available.byName[d]; !ok {
				l.errors = append(l.errors, fmt.Sprintf("Not available dependency: %q in %s", d, p.Name()))
				continue
			}
			if !seen[d] {
				seen[d] = true
				depends = append(depends, d)
			}
		}
		if hs := p.String("hook_source"); hs != "" {
			l.hookSources = append(l.hookSources, hs)
		}
		add(p)
	}
	for _, d := range depends {
		p := available.byName[d]
		add(p)
		if p.String("rtp") != "" {
			if hs := p.String("hook_source"); hs != "" {
				l.hookSources = append(l.hookSources, hs)
			}
		}
	}
	l.rtps = slices.Insert(l.rtps, slices.Index(l.rtps, runtimePath), rtpDir)
	l.rtps = append(l.rtps, rtpDir+"/after")
	return l, nil
}

func vimQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// startupHead is the start of startup.vim: the check of the state version,
// the state, the runtimepath, and g:goda#workspace (the Go modules of the
// plugins, that goda#build() of goda.vim builds their binaries with).
func startupHead(stateVersion int, runtimepath string, available []Plugin) []string {
	var modules []string
	for _, p := range available {
		if path := p.String("path"); path != "" && exists(filepath.Join(path, "go.mod")) {
			modules = append(modules, vimQuote(path))
		}
	}
	return []string{
		fmt.Sprintf(`if g:goda_pm._state_version !=# %d | throw "State version error" | endif`, stateVersion),
		"let [" +
			"g:goda_pm.state.plugins," +
			"g:goda_pm.ftplugin," +
			"g:goda_pm.state.options," +
			"g:goda_pm.state.check_files," +
			"g:goda_pm.state.multiple_hooks," +
			"g:goda_pm.settings.extra_args" +
			"] = g:goda_pm.cache._state",
		"let &runtimepath = " + vimQuote(runtimepath),
		"let g:goda#workspace = [" + strings.Join(modules, ", ") + "]",
	}
}

// readFtpluginHooksFiles adds the ftplugins of the hooks files of the
// configuration (HooksFiles) to its Ftplugins.
func (m *stateMaker) readFtpluginHooksFiles() {
	for _, hf := range m.ret.HooksFiles {
		if parsed, ok := m.readHooks(hf, "hooks file: ", ""); ok {
			MergeFtplugins(m.ret.Ftplugins, ftpluginOf(parsed["ftplugin"]))
		}
	}
}

// configCheckFiles are the files of the configuration that
// goda_pm#check_files() checks: its CheckFiles, the Go files of the
// configuration directory, and its HooksFiles.
func configCheckFiles(ret *ConfigReturn, configDir string) []string {
	files := slices.Clone(ret.CheckFiles)
	if configDir != "" {
		for _, pat := range []string{"*.go", "go.mod"} {
			m, _ := filepath.Glob(filepath.Join(configDir, pat))
			slices.Sort(m)
			files = append(files, m...)
		}
	}
	return append(files, ret.HooksFiles...)
}

// filetypeOff turns the filetype detection and plugins off if the editor
// has them on, as dpp.vim does.
func filetypeOff(vs vimState) []string {
	var lines []string
	if vs.DidLoadFiletypes {
		lines = append(lines, "filetype off")
	}
	if vs.DidLoadFtplugin {
		lines = append(lines, "filetype plugin indent off")
	}
	return lines
}

var (
	blankVim = regexp.MustCompile(`^\s*$|^\s*"`)
	blankLua = regexp.MustCompile(`^\s*$|^\s*--`)
)

// inlineVimrcs are the lines of the inline vimrcs (Options.InlineVimrcs)
// without blank and comment lines, and their files for check_files. Those
// of Lua are left out without Lua.
func (m *stateMaker) inlineVimrcs() (lines, files []string, err error) {
	for _, vimrc := range m.options.InlineVimrcs {
		full, err := Expand(m.ctx, m.h, vimrc)
		if err != nil {
			return nil, nil, err
		}
		b, err := os.ReadFile(full)
		if err != nil {
			m.printError("inlineVimrcs: %v", err)
			continue
		}
		lines = append(lines, inlineLines(full, string(b), m.vs.Lua)...)
		files = append(files, full)
	}
	return lines, files, nil
}

// inlineLines are the lines of an inline vimrc for startup.vim.
func inlineLines(path, content string, lua bool) []string {
	src := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if filepath.Ext(path) != ".lua" {
		return slices.DeleteFunc(src, blankVim.MatchString)
	}
	if !lua {
		return nil
	}
	lines := append([]string{"lua <<EOF"}, slices.DeleteFunc(src, blankLua.MatchString)...)
	return append(lines, "EOF")
}

// pluginHooks are the hook_add of the available plugins, and their hooks
// files for check_files; it adds their ftplugins to ftplugins.
func pluginHooks(available []Plugin, ftplugins map[string]string) (adds, files []string) {
	for _, p := range available {
		files = append(files, p.Strings("hooks_file")...)
		if ha := p.String("hook_add"); ha != "" {
			adds = append(adds, ha)
		}
		if p.Has("ftplugin") {
			MergeFtplugins(ftplugins, ftpluginOf(p["ftplugin"]))
		}
	}
	return adds, files
}

// readMultipleHook sets the hooks of mh from its hooks file, if any.
func (m *stateMaker) readMultipleHook(mh MultipleHook) MultipleHook {
	if mh.HooksFile == "" {
		return mh
	}
	parsed, ok := m.readHooks(mh.HooksFile, "hooks file: ", "")
	if !ok {
		return mh
	}
	if v := parsed.String("hook_add"); v != "" {
		mh.HookAdd = v
	}
	if v := parsed.String("hook_source"); v != "" {
		mh.HookSource = v
	}
	if v := parsed.String("hook_post_source"); v != "" {
		mh.HookPostSource = v
	}
	return mh
}

// splitMultipleHooks takes the hooks that run at startup out of the
// multiple hooks: hook_add if all the plugins are available, hook_source if
// all are loaded at startup (nonLazy). The others stay for goda_pm#source().
func splitMultipleHooks(mhs []MultipleHook, available map[string]Plugin, nonLazy map[string]bool) (adds, sources []string, rest []MultipleHook) {
	all := func(mh MultipleHook, has func(string) bool) bool {
		for _, n := range mh.Plugins {
			if !has(n) {
				return false
			}
		}
		return true
	}
	isAvailable := func(n string) bool { _, ok := available[n]; return ok }
	isNonLazy := func(n string) bool { return nonLazy[n] }
	rest = make([]MultipleHook, 0, len(mhs))
	for _, mh := range mhs {
		if mh.HookAdd != "" && all(mh, isAvailable) {
			adds = append(adds, mh.HookAdd)
			mh.HookAdd = ""
		}
		if mh.HookSource != "" && all(mh, isNonLazy) {
			sources = append(sources, mh.HookSource)
			mh.HookSource = ""
		}
		mh.HookAdd = continuation.ReplaceAllString(mh.HookAdd, "")
		mh.HookSource = continuation.ReplaceAllString(mh.HookSource, "")
		mh.HookPostSource = continuation.ReplaceAllString(mh.HookPostSource, "")
		rest = append(rest, mh)
	}
	return adds, sources, rest
}

// existingFiles are the check files that exist, once each.
func (m *stateMaker) existingFiles(checkFiles []string) ([]string, error) {
	existing := []string{}
	seen := map[string]bool{}
	for _, f := range checkFiles {
		full, err := Expand(m.ctx, m.h, f)
		if err != nil {
			return nil, err
		}
		if exists(full) && !seen[f] {
			seen[f] = true
			existing = append(existing, f)
		}
	}
	return existing, nil
}

// writeFile writes the lines, without carriage returns.
func writeFile(path string, lines []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data := strings.ReplaceAll(strings.Join(lines, "\n"), "\r", "")
	return os.WriteFile(path, []byte(data), 0o644)
}

// writeStateVim writes state.vim: the state as JSON, that
// goda_pm#min#load_state() reads with json_decode().
func writeStateVim(path string, state []any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(state); err != nil {
		return err
	}
	return os.WriteFile(path, bytes.TrimRight(buf.Bytes(), "\n"), 0o644)
}

// writeFtplugins writes the ftplugin files of the merged directory.
func (m *stateMaker) writeFtplugins() error {
	gv, err := m.h.Call(m.ctx, "goda_pm#util#_generate_ftplugin", m.rtpDir, m.ret.Ftplugins)
	if err != nil {
		return err
	}
	var generated map[string][]string
	if err := sdk.Decode(gv, &generated); err != nil {
		return err
	}
	for path, lines := range generated {
		if err := writeFile(path, lines); err != nil {
			return err
		}
	}
	return nil
}

// statePlugins are the plugins as state.vim has them: without what only
// MakeState uses, which Vim reads at every startup for nothing. The hook_add
// of those of inStartup is in startup.vim (that of the others, not installed
// yet, is run when they are), ftplugin in the ftplugin files of the merged
// directory, and the lua_* hooks are hook_* ones (or not run, without Lua).
func statePlugins(plugins map[string]Plugin, inStartup map[string]bool) map[string]Plugin {
	out := make(map[string]Plugin, len(plugins))
	for name, p := range plugins {
		q := make(Plugin, len(p))
		for k, v := range p {
			if k == "hook_add" && inStartup[name] || k == "ftplugin" || strings.HasPrefix(k, "lua_") {
				continue
			}
			q[k] = v
		}
		out[name] = q
	}
	return out
}
