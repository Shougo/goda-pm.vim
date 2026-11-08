// Package pm is goda-pm.vim, the successor of dpp.vim: a plugin manager.
// A configuration (main.go) adds the protocols and the exts, and gives the
// plugins (from TOML files, as dpp-ext-toml's, or from Go), and serves the
// plugin.
//
//	b := pm.New()
//	b.AddProtocol(git.New())
//	b.AddExt(installer.New())
//	b.SetConfig(func(ctx context.Context, c *pm.ConfigArgs) (*pm.ConfigReturn, error) {
//		t, err := toml.Load(ctx, c.Host, "$BASE_DIR/dpp.toml", nil)
//		...
//	})
//	b.Main()
//
// The startup does not need the binary: goda_pm#min#load_state() sources
// the state the binary made (goda_pm#make_state()).
package pm

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"

	"github.com/Shougo/goda.vim/host"
	"github.com/Shougo/goda.vim/sdk"
)

// PluginName is the name of the plugin: goda#request('pm', ...).
const PluginName = "pm"

// ScriptVersion is the version of the Vim script the binary goes with
// (s:version of autoload/goda_pm.vim).
const ScriptVersion = 2

// Builder collects the protocols, the exts and the configuration.
type Builder struct {
	protocols []Protocol
	exts      map[string]Ext
	config    ConfigFunc
	errs      []string
}

// New returns an empty Builder.
func New() *Builder { return &Builder{exts: map[string]Ext{}} }

// AddProtocol adds a protocol under its Name. The protocols detect plugins
// in the order they are added (unless Options.Protocols says otherwise).
func (b *Builder) AddProtocol(p Protocol) *Builder {
	for _, q := range b.protocols {
		if q.Name() == p.Name() {
			b.errs = append(b.errs, fmt.Sprintf("protocol %q added twice", p.Name()))
		}
	}
	b.protocols = append(b.protocols, p)
	return b
}

// AddExt adds an ext under its Name.
func (b *Builder) AddExt(e Ext) *Builder {
	if _, dup := b.exts[e.Name()]; dup {
		b.errs = append(b.errs, fmt.Sprintf("ext %q added twice", e.Name()))
	}
	b.exts[e.Name()] = e
	return b
}

// SetConfig sets the configuration, which goda_pm#make_state() runs.
func (b *Builder) SetConfig(f ConfigFunc) *Builder {
	b.config = f
	return b
}

// Server is the plugin of a configuration.
type Server struct {
	protocolList []Protocol
	exts         map[string]Ext
	config       ConfigFunc
	stateMu      sync.Mutex
}

// Server is the plugin of the configuration, or the errors in it.
func (b *Builder) Server() (*Server, error) {
	if len(b.errs) > 0 {
		return nil, fmt.Errorf("goda-pm: %s", strings.Join(b.errs, "; "))
	}
	if b.config == nil {
		return nil, fmt.Errorf("goda-pm: no configuration (SetConfig)")
	}
	return &Server{protocolList: b.protocols, exts: b.exts, config: b.config}, nil
}

// Main serves the plugin, with the other plugins given, as the whole
// program: the last line of main.go. A configuration error does not end
// it: goda_pm#make_state() shows it, and the other plugins work.
func (b *Builder) Main(others ...sdk.Plugin) {
	host.Main(append([]sdk.Plugin{b.MainPlugin()}, others...)...)
}

// MainPlugin is the plugin, for the Main of another binary: with an invalid
// configuration, one that tells it.
func (b *Builder) MainPlugin() sdk.Plugin {
	s, err := b.Server()
	if err != nil {
		log.Print(err)
		return brokenPlugin{err}
	}
	return s
}

type brokenPlugin struct{ err error }

func (brokenPlugin) Name() string       { return PluginName }
func (brokenPlugin) ScriptVersion() int { return ScriptVersion }

func (p brokenPlugin) Handlers() map[string]sdk.Handler {
	hs := map[string]sdk.Handler{}
	for name := range (&Server{}).Handlers() {
		hs[name] = func(context.Context, sdk.Host, []any) (any, error) { return nil, p.err }
	}
	hs["status"] = func(context.Context, sdk.Host, []any) (any, error) { return p.err.Error(), nil }
	return hs
}

func (s *Server) Name() string       { return PluginName }
func (s *Server) ScriptVersion() int { return ScriptVersion }

func (s *Server) Handlers() map[string]sdk.Handler {
	return map[string]sdk.Handler{
		"status":    func(context.Context, sdk.Host, []any) (any, error) { return "", nil },
		"makeState": s.handleMakeState,
		"extAction": s.handleExtAction,
		"exts":      s.handleExts,
	}
}

func (s *Server) protocols(h sdk.Host, basePath string, o *Options) *Protocols {
	ps := &Protocols{host: h, basePath: basePath, options: o, byName: map[string]Protocol{}}
	for _, p := range s.protocolList {
		ps.order = append(ps.order, p.Name())
		ps.byName[p.Name()] = p
	}
	return ps
}

// done tells the Vim side that the call id is done (goda_pm#_done()): it
// gives the result to who waits for it, or shows the error.
func done(ctx context.Context, h sdk.Host, id int, result any, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if result == nil {
		result = map[string]any{}
	}
	if derr := h.Notify(ctx, "goda_pm#_done", id, result, msg); derr != nil {
		log.Printf("goda-pm: %v", derr)
	}
}

// makeState(args, id) makes the state, in the background: goda_pm#_done()
// tells when it is done.
func (s *Server) handleMakeState(ctx context.Context, h sdk.Host, params []any) (any, error) {
	var sa stateArgs
	id := 0
	if err := sdk.DecodeArgs(params, &sa, sdk.Optional(&id)); err != nil {
		return nil, err
	}
	go func() {
		err := s.makeState(ctx, h, sa)
		done(ctx, h, id, nil, err)
	}()
	return nil, nil
}

// settings are those of the state (g:goda_pm.settings).
func settings(ctx context.Context, h sdk.Host) (stateArgs, error) {
	v, err := h.Eval(ctx, "#{base_path: get(g:goda_pm.settings, 'base_path', ''),"+
		" name: goda_pm#util#_get_name(), extra_args: get(g:goda_pm.settings, 'extra_args', {})}")
	if err != nil {
		return stateArgs{}, err
	}
	var sa stateArgs
	err = sdk.Decode(v, &sa)
	return sa, err
}

// extAction(ext, action, params, id) runs the action of the ext, in the
// background: goda_pm#_done() gets its result.
func (s *Server) handleExtAction(ctx context.Context, h sdk.Host, params []any) (any, error) {
	var extName, actionName string
	var ap Params
	id := 0
	if err := sdk.DecodeArgs(params, &extName, &actionName, sdk.Optional(&ap), sdk.Optional(&id)); err != nil {
		return nil, err
	}
	go func() {
		r, err := s.extAction(ctx, h, extName, actionName, ap)
		done(ctx, h, id, r, err)
	}()
	return nil, nil
}

func (s *Server) extAction(ctx context.Context, h sdk.Host, extName, actionName string, ap Params) (any, error) {
	e, ok := s.exts[extName]
	if !ok {
		return nil, fmt.Errorf("goda-pm: no such ext: %q", extName)
	}
	act, ok := e.Actions()[actionName]
	if !ok {
		var names []string
		for n := range e.Actions() {
			names = append(names, n)
		}
		slices.Sort(names)
		return nil, fmt.Errorf("goda-pm: no such action of the ext %q: %q (%s)",
			extName, actionName, strings.Join(names, ", "))
	}
	sa, err := settings(ctx, h)
	if err != nil {
		return nil, err
	}
	options := DefaultOptions()
	v, err := h.Eval(ctx, "get(g:goda_pm.state, 'options', {})")
	if err != nil {
		return nil, err
	}
	if m, ok := v.(map[string]any); ok && len(m) > 0 {
		if err := sdk.Decode(m, &options); err != nil {
			return nil, err
		}
	}
	if ap == nil {
		ap = Params{}
	}
	args := ActionArgs{
		Host:         h,
		Options:      options,
		ExtParams:    options.ExtParams[extName],
		ActionParams: ap,
		Protocols:    s.protocols(h, sa.BasePath, &options),
		BasePath:     sa.BasePath,
		Name:         sa.Name,
		MakeState: func(ctx context.Context) error {
			sa, err := settings(ctx, h)
			if err != nil {
				return err
			}
			return s.makeState(ctx, h, sa)
		},
	}
	r, err := act.Run(ctx, args)
	if err != nil {
		return nil, err
	}
	event := "GodaPm:extActionPost:" + extName + ":" + actionName
	if err := h.Cmd(ctx, "if exists('#User#"+event+"') | doautocmd <nomodeline> User "+event+" | endif"); err != nil {
		return nil, err
	}
	return r, nil
}

// exts() are the exts and their actions: {ext: [action]}.
func (s *Server) handleExts(context.Context, sdk.Host, []any) (any, error) {
	out := map[string]any{}
	for name, e := range s.exts {
		var names []any
		for _, n := range slices.Sorted(func(yield func(string) bool) {
			for k := range e.Actions() {
				if !yield(k) {
					return
				}
			}
		}) {
			names = append(names, n)
		}
		out[name] = names
	}
	return out, nil
}
