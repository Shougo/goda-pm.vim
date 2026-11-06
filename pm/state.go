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
func (s *Server) makeState(ctx context.Context, h sdk.Host, sa stateArgs) error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	var vs vimState
	v, err := h.Eval(ctx, strings.ReplaceAll(vimStateExpr, "\n\t\\", ""))
	if err != nil {
		return err
	}
	if err := sdk.Decode(v, &vs); err != nil {
		return err
	}
	basePath, name := sa.BasePath, sa.Name
	if sa.ExtraArgs == nil {
		sa.ExtraArgs = map[string]any{}
	}

	options := DefaultOptions()
	ps := s.protocols(h, basePath, &options)
	c := &ConfigArgs{Host: h, BasePath: basePath, Name: name, ExtraArgs: sa.ExtraArgs,
		Options: &options, protocols: ps}
	if s.config == nil {
		return errors.New("no configuration: call SetConfig() of the builder")
	}
	ret, err := s.config(ctx, c)
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	if ret == nil {
		ret = &ConfigReturn{}
	}
	if ret.Ftplugins == nil {
		ret.Ftplugins = map[string]string{}
	}

	checkIf := func(p Plugin) (bool, error) {
		cond, ok := p["if"]
		if !ok {
			return true, nil
		}
		if expr, ok := cond.(string); ok {
			r, err := h.Call(ctx, "eval", expr)
			if err != nil {
				return false, fmt.Errorf("%s: if: %w", p.Name(), err)
			}
			return Truthy(r), nil
		}
		return Truthy(cond), nil
	}

	recordPlugins := map[string]Plugin{}
	var available []Plugin
	availableByName := map[string]Plugin{}
	for _, base := range ret.Plugins {
		p := base.Clone()
		if err := detectPlugin(ctx, ps, p); err != nil {
			s.printError(ctx, h, "%v", err)
		}
		if ret.Groups != nil && p.Has("group") {
			for _, g := range p.Strings("group") {
				opts, ok := ret.Groups[g]
				if !ok {
					s.printError(ctx, h, "Not available group: %q in %s", g, p.Name())
					continue
				}
				p = Merge(opts, p)
			}
		}
		if p.Has("hooks_file") {
			for _, hf := range p.Strings("hooks_file") {
				lines, err := readHooksFile(ctx, h, hf)
				if err != nil {
					s.printError(ctx, h, "%s: hooks_file: %v", p.Name(), err)
					continue
				}
				parsed, err := ParseHooksFile(options.HooksFileMarker, lines, hf)
				if err != nil {
					s.printError(ctx, h, "%s: %v", p.Name(), err)
					continue
				}
				p = Merge(p, parsed)
			}
		}
		if p.String("path") == "" {
			s.printError(ctx, h, "\"path\" must be set: %q", p.Name())
		}
		p = InitPlugin(p, basePath, vs.Lua)
		if p.Name() == "" {
			continue
		}
		if _, dup := recordPlugins[p.Name()]; dup {
			s.printError(ctx, h, "Duplicated plugin is detected: %q", p.Name())
			if i := slices.IndexFunc(available, func(q Plugin) bool { return q.Name() == p.Name() }); i >= 0 {
				available = slices.Delete(available, i, i+1)
				delete(availableByName, p.Name())
			}
		}
		recordPlugins[p.Name()] = p
		if isDir(p.String("path")) {
			ok, err := checkIf(p)
			if err != nil {
				s.printError(ctx, h, "%v", err)
			}
			if ok {
				available = append(available, p)
				availableByName[p.Name()] = p
			}
		}
	}

	stateDir := filepath.Join(basePath, name)
	rtpDir := basePath + "/" + name + "/.goda_pm"
	if isDir(rtpDir) {
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
	} else if err := os.MkdirAll(rtpDir, 0o755); err != nil {
		return err
	}

	rtps := slices.Clone(vs.Rtps)
	runtimeIndex := slices.Index(rtps, vs.RuntimePath)
	if runtimeIndex < 0 {
		return errors.New("Vim runtime path is not found in runtimepath")
	}
	addRtp := func(p Plugin) {
		rtp := p.String("rtp")
		if rtp == "" {
			return
		}
		if !p.Bool("merged") {
			rtps = slices.Insert(rtps, runtimeIndex, rtp)
			if after := rtp + "/after"; isDir(after) {
				rtps = slices.Insert(rtps, slices.Index(rtps, vs.RuntimePath)+1, after)
			}
		}
		p["sourced"] = true
	}
	var nonLazy []Plugin
	for _, p := range available {
		if p.String("rtp") != "" && !p.Bool("lazy") {
			nonLazy = append(nonLazy, p)
		}
	}
	slices.SortStableFunc(nonLazy, func(a, b Plugin) int { return b.Int("priority") - a.Int("priority") })
	var hookSources []string
	var depends []string
	seenDepend := map[string]bool{}
	for _, p := range nonLazy {
		for _, d := range p.Strings("depends") {
			if _, ok := availableByName[d]; !ok {
				s.printError(ctx, h, "Not available dependency: %q in %s", d, p.Name())
				continue
			}
			if !seenDepend[d] {
				seenDepend[d] = true
				depends = append(depends, d)
			}
		}
		if hs := p.String("hook_source"); hs != "" {
			hookSources = append(hookSources, hs)
		}
		addRtp(p)
	}
	for _, d := range depends {
		p := availableByName[d]
		addRtp(p)
		if p.String("rtp") != "" {
			if hs := p.String("hook_source"); hs != "" {
				hookSources = append(hookSources, hs)
			}
		}
	}
	rtps = slices.Insert(rtps, slices.Index(rtps, vs.RuntimePath), rtpDir)
	rtps = append(rtps, rtpDir+"/after")
	jv, err := h.Call(ctx, "goda_pm#util#_join_rtp", rtps, vs.InitRuntimepath, rtpDir)
	if err != nil {
		return err
	}
	newRuntimepath, _ := jv.(string)

	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	startup := []string{
		fmt.Sprintf(`if g:goda_pm._state_version !=# %d | throw "State version error" | endif`, vs.StateVersion),
		"let [" +
			"g:goda_pm.state.plugins," +
			"g:goda_pm.ftplugin," +
			"g:goda_pm.state.options," +
			"g:goda_pm.state.check_files," +
			"g:goda_pm.state.multiple_hooks," +
			"g:goda_pm.settings.extra_args" +
			"] = g:goda_pm.cache._state",
		"let &runtimepath = " + quote(newRuntimepath),
	}

	for _, hf := range ret.HooksFiles {
		lines, err := readHooksFile(ctx, h, hf)
		if err != nil {
			s.printError(ctx, h, "hooks file: %v", err)
			continue
		}
		parsed, err := ParseHooksFile(options.HooksFileMarker, lines, hf)
		if err != nil {
			s.printError(ctx, h, "%v", err)
			continue
		}
		MergeFtplugins(ret.Ftplugins, ftpluginOf(parsed["ftplugin"]))
	}

	checkFiles := slices.Clone(ret.CheckFiles)
	if vs.ConfigDir != "" {
		for _, pat := range []string{"*.go", "go.mod"} {
			m, _ := filepath.Glob(filepath.Join(vs.ConfigDir, pat))
			slices.Sort(m)
			checkFiles = append(checkFiles, m...)
		}
	}
	checkFiles = append(checkFiles, ret.HooksFiles...)

	if vs.DidLoadFiletypes {
		startup = append(startup, "filetype off")
	}
	if vs.DidLoadFtplugin {
		startup = append(startup, "filetype plugin indent off")
	}
	startup = append(startup, ret.StateLines...)

	blankVim := regexp.MustCompile(`^\s*$|^\s*"`)
	blankLua := regexp.MustCompile(`^\s*$|^\s*--`)
	for _, vimrc := range options.InlineVimrcs {
		full, err := Expand(ctx, h, vimrc)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(full)
		if err != nil {
			s.printError(ctx, h, "inlineVimrcs: %v", err)
			continue
		}
		lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
		if filepath.Ext(full) == ".lua" {
			if vs.Lua {
				startup = append(startup, "lua <<EOF")
				for _, l := range lines {
					if !blankLua.MatchString(l) {
						startup = append(startup, l)
					}
				}
				startup = append(startup, "EOF")
			}
		} else {
			for _, l := range lines {
				if !blankVim.MatchString(l) {
					startup = append(startup, l)
				}
			}
		}
		checkFiles = append(checkFiles, full)
	}

	for _, p := range available {
		checkFiles = append(checkFiles, p.Strings("hooks_file")...)
		if ha := p.String("hook_add"); ha != "" {
			startup = append(startup, ha)
		}
		if p.Has("ftplugin") {
			MergeFtplugins(ret.Ftplugins, ftpluginOf(p["ftplugin"]))
		}
	}

	nonLazyNames := map[string]bool{}
	for _, p := range nonLazy {
		nonLazyNames[p.Name()] = true
	}
	multipleHooks := make([]MultipleHook, 0, len(ret.MultipleHooks))
	for _, mh := range ret.MultipleHooks {
		if mh.HooksFile != "" {
			lines, err := readHooksFile(ctx, h, mh.HooksFile)
			if err != nil {
				s.printError(ctx, h, "hooks file: %v", err)
			} else if parsed, err := ParseHooksFile(options.HooksFileMarker, lines, mh.HooksFile); err != nil {
				s.printError(ctx, h, "%v", err)
			} else {
				if v := parsed.String("hook_add"); v != "" {
					mh.HookAdd = v
				}
				if v := parsed.String("hook_source"); v != "" {
					mh.HookSource = v
				}
				if v := parsed.String("hook_post_source"); v != "" {
					mh.HookPostSource = v
				}
			}
		}
		all := func(names map[string]bool) bool {
			for _, n := range mh.Plugins {
				if !names[n] {
					return false
				}
			}
			return true
		}
		availableNames := map[string]bool{}
		for n := range availableByName {
			availableNames[n] = true
		}
		if mh.HookAdd != "" && all(availableNames) {
			startup = append(startup, mh.HookAdd)
			mh.HookAdd = ""
		}
		if mh.HookSource != "" && all(nonLazyNames) {
			hookSources = append(hookSources, mh.HookSource)
			mh.HookSource = ""
		}
		mh.HookAdd = continuation.ReplaceAllString(mh.HookAdd, "")
		mh.HookSource = continuation.ReplaceAllString(mh.HookSource, "")
		mh.HookPostSource = continuation.ReplaceAllString(mh.HookPostSource, "")
		multipleHooks = append(multipleHooks, mh)
	}
	startup = append(startup, hookSources...)

	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	content := strings.ReplaceAll(strings.Join(startup, "\n"), "\r", "")
	if err := os.WriteFile(filepath.Join(stateDir, "startup.vim"), []byte(content), 0o644); err != nil {
		return err
	}

	var existing []string
	seen := map[string]bool{}
	for _, f := range checkFiles {
		full, err := Expand(ctx, h, f)
		if err != nil {
			return err
		}
		if exists(full) && !seen[f] {
			seen[f] = true
			existing = append(existing, f)
		}
	}
	if existing == nil {
		existing = []string{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode([]any{recordPlugins, map[string]any{}, options, existing, multipleHooks, sa.ExtraArgs}); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stateDir, "state.vim"), bytes.TrimRight(buf.Bytes(), "\n"), 0o644); err != nil {
		return err
	}

	if err := s.mergePlugins(ctx, h, basePath, name, rtpDir, options.SkipMergeFilenamePattern, available, vs.Windows); err != nil {
		return err
	}

	gv, err := h.Call(ctx, "goda_pm#util#_generate_ftplugin", rtpDir, ret.Ftplugins)
	if err != nil {
		return err
	}
	var generated map[string][]string
	if err := sdk.Decode(gv, &generated); err != nil {
		return err
	}
	for path, lines := range generated {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		data := strings.ReplaceAll(strings.Join(lines, "\n"), "\r", "")
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			return err
		}
	}

	return h.Cmd(ctx, "if exists('#User#GodaPm:makeStatePost') | doautocmd <nomodeline> User GodaPm:makeStatePost | endif")
}
