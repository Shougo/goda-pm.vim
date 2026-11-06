package pm

import (
	"context"
	"time"

	"github.com/Shougo/goda.vim/sdk"
)

// Command is a command a protocol gives the installer to run: an external
// program (Command and Args), or a function of the protocol (Func). It runs
// in the directory of the plugin when it exists.
type Command struct {
	Command string
	Args    []string
	// Func is run instead of the program when set: out writes a line of
	// its output.
	Func func(ctx context.Context, out func(string)) error
}

// ProtocolArgs are what a protocol works with.
type ProtocolArgs struct {
	Host   sdk.Host
	Plugin Plugin
	// Params are the params of the protocol (Options.ProtocolParams).
	Params Params
	// BasePath is the base directory of the state: the plugins go in
	// BasePath/repos.
	BasePath string
}

// Protocol is how plugins are got: git, http, ... as dpp.vim's protocols.
// Embed BaseProtocol for the methods it does not have.
type Protocol interface {
	Name() string
	// Detect is what the protocol adds to the plugin (path, url, ...)
	// when it gets it, or nil.
	Detect(ctx context.Context, a ProtocolArgs) (Plugin, error)
	// URL is the URL the plugin is got from.
	URL(ctx context.Context, a ProtocolArgs) (string, error)
	// SyncCommands install or update the plugin.
	SyncCommands(ctx context.Context, a ProtocolArgs) ([]Command, error)
	// LogCommands print the changes from oldRev to newRev.
	LogCommands(ctx context.Context, a ProtocolArgs, oldRev, newRev string) ([]Command, error)
	// RevisionLockCommands check out the rev of the plugin.
	RevisionLockCommands(ctx context.Context, a ProtocolArgs) ([]Command, error)
	// RollbackCommands go back to rev.
	RollbackCommands(ctx context.Context, a ProtocolArgs, rev string) ([]Command, error)
	// DiffCommands print the changes of the documents from oldRev to
	// newRev.
	DiffCommands(ctx context.Context, a ProtocolArgs, oldRev, newRev string) ([]Command, error)
	// ChangesCountCommands print the number of the changes from oldRev to
	// newRev.
	ChangesCountCommands(ctx context.Context, a ProtocolArgs, oldRev, newRev string) ([]Command, error)
	// CheckRemoteCommands print the changes that an update would get.
	CheckRemoteCommands(ctx context.Context, a ProtocolArgs) ([]Command, error)
	// Revision is the revision installed.
	Revision(ctx context.Context, a ProtocolArgs) (string, error)
	// RemoteRevision is the revision an update would get.
	RemoteRevision(ctx context.Context, a ProtocolArgs) (string, error)
	// DateFromRevision is the date of rev, or the zero time.
	DateFromRevision(ctx context.Context, a ProtocolArgs, rev string) (time.Time, error)
	// Histories are the revisions from start back to end.
	Histories(ctx context.Context, a ProtocolArgs, start, end string) ([]string, error)
}

// BaseProtocol has the methods of a Protocol that do nothing.
type BaseProtocol struct{}

func (BaseProtocol) URL(context.Context, ProtocolArgs) (string, error) { return "", nil }
func (BaseProtocol) SyncCommands(context.Context, ProtocolArgs) ([]Command, error) {
	return nil, nil
}
func (BaseProtocol) LogCommands(context.Context, ProtocolArgs, string, string) ([]Command, error) {
	return nil, nil
}
func (BaseProtocol) RevisionLockCommands(context.Context, ProtocolArgs) ([]Command, error) {
	return nil, nil
}
func (BaseProtocol) RollbackCommands(context.Context, ProtocolArgs, string) ([]Command, error) {
	return nil, nil
}
func (BaseProtocol) DiffCommands(context.Context, ProtocolArgs, string, string) ([]Command, error) {
	return nil, nil
}
func (BaseProtocol) ChangesCountCommands(context.Context, ProtocolArgs, string, string) ([]Command, error) {
	return nil, nil
}
func (BaseProtocol) CheckRemoteCommands(context.Context, ProtocolArgs) ([]Command, error) {
	return nil, nil
}
func (BaseProtocol) Revision(context.Context, ProtocolArgs) (string, error) { return "", nil }
func (BaseProtocol) RemoteRevision(context.Context, ProtocolArgs) (string, error) {
	return "", nil
}
func (BaseProtocol) DateFromRevision(context.Context, ProtocolArgs, string) (time.Time, error) {
	return time.Time{}, nil
}
func (BaseProtocol) Histories(context.Context, ProtocolArgs, string, string) ([]string, error) {
	return nil, nil
}

// Protocols are the protocols of the options.
type Protocols struct {
	host     sdk.Host
	basePath string
	options  *Options
	order    []string
	byName   map[string]Protocol
}

// Names are the protocols that detect plugins, in order.
func (ps *Protocols) Names() []string {
	if len(ps.options.Protocols) == 0 {
		return ps.order
	}
	var out []string
	for _, n := range ps.options.Protocols {
		if _, ok := ps.byName[n]; ok {
			out = append(out, n)
		}
	}
	return out
}

// Get is the protocol name.
func (ps *Protocols) Get(name string) (Protocol, bool) {
	p, ok := ps.byName[name]
	return p, ok
}

// Args are the arguments of the protocol name for the plugin.
func (ps *Protocols) Args(name string, p Plugin) ProtocolArgs {
	return ProtocolArgs{Host: ps.host, Plugin: p, Params: ps.options.ProtocolParams[name], BasePath: ps.basePath}
}

// Of is the protocol of the plugin (its key "protocol") and its arguments.
func (ps *Protocols) Of(p Plugin) (Protocol, ProtocolArgs, bool) {
	name := p.String("protocol")
	pr, ok := ps.byName[name]
	if !ok {
		return nil, ProtocolArgs{}, false
	}
	return pr, ps.Args(name, p), true
}

// Ext is an extension with actions, run with goda_pm#sync_ext_action() and
// goda_pm#async_ext_action(), as dpp.vim's exts.
type Ext interface {
	Name() string
	Actions() map[string]Action
}

// Action is an action of an ext.
type Action struct {
	Description string
	Run         func(ctx context.Context, a ActionArgs) (any, error)
}

// ActionArgs are what an action works with.
type ActionArgs struct {
	Host sdk.Host
	// Options are those of the state.
	Options Options
	// ExtParams are the params of the ext (Options.ExtParams).
	ExtParams Params
	// ActionParams are the params of the call.
	ActionParams Params
	Protocols    *Protocols
	// BasePath and Name are those of the state.
	BasePath string
	Name     string
	// MakeState makes the state again (after plugins are updated).
	MakeState func(ctx context.Context) error
}
