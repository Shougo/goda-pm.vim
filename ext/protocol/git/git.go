// Package git is the protocol "git" of goda-pm.vim, as dpp-protocol-git:
// the plugins of git repositories ("Shougo/goda.vim" is one of GitHub).
package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Shougo/goda-pm.vim/pm"
)

// Params are the protocol params (protocolParams.git), as
// dpp-protocol-git's.
type Params struct {
	// CloneDepth is the depth of a clone (all of the history when 0).
	CloneDepth int `json:"cloneDepth"`
	// CommandPath is the git command.
	CommandPath string `json:"commandPath"`
	// DefaultHubSite is the host of a repo without one ("user/name").
	DefaultHubSite string `json:"defaultHubSite"`
	// DefaultProtocol is that of the URL: "https" or "ssh".
	DefaultProtocol string `json:"defaultProtocol"`
	DefaultRemote   string `json:"defaultRemote"`
	// EnableCredentialHelper uses the credential helper of git.
	EnableCredentialHelper bool `json:"enableCredentialHelper"`
	// EnablePartialClone clones without the blobs of the history.
	EnablePartialClone bool `json:"enablePartialClone"`
	EnableSSLVerify    bool `json:"enableSSLVerify"`
	// PullArgs are the arguments of the pull of an update.
	PullArgs []string `json:"pullArgs"`
}

// DefaultParams are the default params.
func DefaultParams() Params {
	return Params{
		CommandPath:     "git",
		DefaultHubSite:  "github.com",
		DefaultProtocol: "https",
		DefaultRemote:   "origin",
		EnableSSLVerify: true,
		PullArgs:        []string{"pull", "--ff", "--ff-only"},
	}
}

// Attrs are the attributes of a plugin (its protocolAttrs).
type Attrs struct {
	GitDefaultBranch string `json:"gitDefaultBranch"`
	GitRemote        string `json:"gitRemote"`
}

// Protocol is the protocol "git".
type Protocol struct{ pm.BaseProtocol }

// New returns the protocol, to add to the builder.
func New() *Protocol { return &Protocol{} }

func (*Protocol) Name() string { return "git" }

func params(a pm.ProtocolArgs) Params {
	p := DefaultParams()
	_ = pm.DecodeParams(a.Params, &p)
	return p
}

func attrs(p pm.Plugin) Attrs {
	var at Attrs
	if m, ok := p["protocolAttrs"].(map[string]any); ok {
		_ = pm.DecodeParams(m, &at)
	}
	return at
}

var notGit = regexp.MustCompile(`//(raw|gist)\.githubusercontent\.com/|/archive/.*\.zip$`)

func (g *Protocol) Detect(ctx context.Context, a pm.ProtocolArgs) (pm.Plugin, error) {
	repo := a.Plugin.String("repo")
	if repo == "" || notGit.MatchString(repo) {
		return nil, nil
	}
	if filepath.IsAbs(repo) || strings.HasPrefix(repo, "/") || strings.HasPrefix(repo, "~") {
		path, err := pm.Expand(ctx, a.Host, repo)
		if err != nil {
			return nil, err
		}
		if gitDir(path) != "" {
			return pm.Plugin{"local": true, "path": path}, nil
		}
	}
	url, _ := g.URL(ctx, a)
	if url == "" {
		return nil, nil
	}
	dir := strings.TrimSuffix(url, ".git")
	dir = regexp.MustCompile(`https:/+|^git@`).ReplaceAllString(dir, "")
	dir = strings.Replace(dir, ":", "/", 1)
	browse := regexp.MustCompile(`^git@github.com:`).ReplaceAllString(url, "https://github.com/")
	browse = regexp.MustCompile(`^git@git.sr.ht:`).ReplaceAllString(browse, "https://git.sr.ht")
	browse = strings.TrimSuffix(browse, ".git")
	return pm.Plugin{"path": a.BasePath + "/repos/" + dir, "url": browse}, nil
}

func (*Protocol) URL(_ context.Context, a pm.ProtocolArgs) (string, error) {
	p := params(a)
	return GitURL(a.Plugin.String("repo"), p.DefaultHubSite, p.DefaultProtocol), nil
}

var (
	sshRe      = regexp.MustCompile(`^git@([^:]+):([^/]+)/(.+)`)
	protocolRe = regexp.MustCompile(`^([^:]+)://([^/]+)/([^/]+)/(.+)`)
	hostRe     = regexp.MustCompile(`^(([^/]+)/)?([^/]+)/(.+)`)
)

// GitURL is the URL of the git repository of repo: "user/name" is one of
// the hub site.
func GitURL(repo, defaultHubSite, defaultProtocol string) string {
	if !strings.Contains(repo, "/") {
		return ""
	}
	protocol, host, user, name := defaultProtocol, defaultHubSite, "", ""
	if m := sshRe.FindStringSubmatch(repo); m != nil {
		protocol, host, user, name = "ssh", m[1], m[2], m[3]
	} else if m := protocolRe.FindStringSubmatch(repo); m != nil {
		protocol, host, user, name = m[1], m[2], m[3], m[4]
	} else if m := hostRe.FindStringSubmatch(repo); m != nil {
		if m[2] != "" {
			host = m[2]
		}
		user, name = m[3], m[4]
	}
	if user == "" || name == "" {
		return ""
	}
	if protocol != "https" && protocol != "ssh" {
		return ""
	}
	var url string
	if protocol == "ssh" {
		url = "git@" + host + ":" + user + "/" + name
	} else {
		url = protocol + "://" + host + "/" + user + "/" + name
	}
	if host == "git.sr.ht" || strings.HasSuffix(url, ".git") {
		return url
	}
	return url + ".git"
}

func initArgs(p Params) []string {
	var args []string
	if !p.EnableCredentialHelper {
		args = append(args, "-c", "credential.helper=", "-c", "core.fsmonitor=false")
	}
	if !p.EnableSSLVerify {
		args = append(args, "-c", "http.sslVerify=false")
	}
	return args
}

func (g *Protocol) SyncCommands(ctx context.Context, a pm.ProtocolArgs) ([]pm.Command, error) {
	path := a.Plugin.String("path")
	if a.Plugin.String("repo") == "" || path == "" {
		return nil, nil
	}
	p := params(a)
	init := initArgs(p)
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		cmds := []pm.Command{{Command: p.CommandPath, Args: append(append([]string{}, init...), "fetch")}}
		if p.CloneDepth <= 0 {
			remote := attrs(a.Plugin).GitRemote
			if remote == "" {
				remote = p.DefaultRemote
			}
			cmds = append(cmds, pm.Command{Command: p.CommandPath,
				Args: append(append([]string{}, init...), "remote", "set-head", remote, "-a")})
		}
		cmds = append(cmds,
			pm.Command{Command: p.CommandPath, Args: append(append([]string{}, init...), p.PullArgs...)},
			pm.Command{Command: p.CommandPath, Args: append(append([]string{}, init...), "submodule", "update", "--init", "--recursive")},
		)
		return cmds, nil
	}
	args := append(append([]string{}, init...), "clone", "--recursive")
	if p.EnablePartialClone {
		args = append(args, "--filter=blob:none")
	}
	if p.CloneDepth > 0 {
		args = append(args, "--depth="+strconv.Itoa(p.CloneDepth))
		if rev := a.Plugin.String("rev"); rev != "" {
			args = append(args, "--branch", rev)
		}
	}
	url, _ := g.URL(ctx, a)
	args = append(args, url, path)
	return []pm.Command{{Command: p.CommandPath, Args: args}}, nil
}

func (*Protocol) RollbackCommands(_ context.Context, a pm.ProtocolArgs, rev string) ([]pm.Command, error) {
	if a.Plugin.String("repo") == "" || a.Plugin.String("path") == "" {
		return nil, nil
	}
	return []pm.Command{{Command: params(a).CommandPath, Args: []string{"reset", "--hard", rev}}}, nil
}

// ready is whether the plugin is a git repository.
func ready(a pm.ProtocolArgs) bool {
	return a.Plugin.String("repo") != "" && a.Plugin.String("path") != "" && gitDir(a.Plugin.String("path")) != ""
}

func (*Protocol) DiffCommands(_ context.Context, a pm.ProtocolArgs, oldRev, newRev string) ([]pm.Command, error) {
	if !ready(a) {
		return nil, nil
	}
	return []pm.Command{{Command: params(a).CommandPath,
		Args: []string{"diff", oldRev + ".." + newRev, "--", "doc", "README", "README.md"}}}, nil
}

func (*Protocol) LogCommands(ctx context.Context, a pm.ProtocolArgs, oldRev, newRev string) ([]pm.Command, error) {
	if oldRev == "" || newRev == "" || !ready(a) {
		return nil, nil
	}
	p := params(a)
	base := output(ctx, a.Plugin.String("path"), p.CommandPath, "merge-base", oldRev, newRev)
	notAncestor := base == oldRev
	rng := oldRev + "^.." + newRev
	if notAncestor {
		rng = oldRev + ".." + newRev
	}
	return []pm.Command{{Command: p.CommandPath,
		Args: []string{"log", rng, "--graph", "--no-show-signature", `--pretty=format:"%h [%cr] %s"`}}}, nil
}

func (*Protocol) RevisionLockCommands(ctx context.Context, a pm.ProtocolArgs) ([]pm.Command, error) {
	if !ready(a) {
		return nil, nil
	}
	p := params(a)
	path := a.Plugin.String("path")
	rev := a.Plugin.String("rev")
	if strings.Contains(rev, "*") {
		rev = firstLine(output(ctx, path, p.CommandPath, "tag", rev, "--list", "--sort", "-version:refname"))
	}
	if rev == "" {
		rev = firstLine(output(ctx, path, p.CommandPath, "symbolic-ref", "--short", "HEAD"))
		if rev == "" || strings.HasPrefix(rev, "fatal: ") {
			rev = defaultBranchName(a.Plugin)
		}
	}
	return []pm.Command{{Command: p.CommandPath, Args: []string{"checkout", "--quiet", "--guess", rev, "--"}}}, nil
}

func (*Protocol) ChangesCountCommands(_ context.Context, a pm.ProtocolArgs, oldRev, newRev string) ([]pm.Command, error) {
	if !ready(a) {
		return nil, nil
	}
	return []pm.Command{{Command: params(a).CommandPath, Args: []string{"rev-list", "--count", oldRev + ".." + newRev}}}, nil
}

var sha1Re = regexp.MustCompile(`^[0-9a-f]{40}$`)

func (*Protocol) Revision(ctx context.Context, a pm.ProtocolArgs) (string, error) {
	path := a.Plugin.String("path")
	if a.Plugin.String("repo") == "" || path == "" {
		return "", nil
	}
	p := params(a)
	if head := firstLine(output(ctx, path, p.CommandPath, "rev-parse", "--verify", "HEAD")); head != "" {
		return head, nil
	}
	if gitDir(path) == "" {
		return "", nil
	}
	if branch := firstLine(output(ctx, path, p.CommandPath, "symbolic-ref", "--short", "HEAD")); branch != "" &&
		!strings.HasPrefix(branch, "fatal: ") {
		for _, ref := range []string{"refs/remotes/" + p.DefaultRemote + "/" + branch, "refs/heads/" + branch, branch} {
			if sha := resolve(ctx, path, p.CommandPath, ref); sha != "" {
				return sha, nil
			}
		}
	}
	return resolve(ctx, path, p.CommandPath, defaultBranchName(a.Plugin)), nil
}

func (*Protocol) RemoteRevision(ctx context.Context, a pm.ProtocolArgs) (string, error) {
	if !ready(a) {
		return "", nil
	}
	p := params(a)
	path := a.Plugin.String("path")
	rev := a.Plugin.String("rev")
	if rev == "" {
		rev = defaultBranch(ctx, a, p)
	}
	for _, ref := range []string{"refs/remotes/" + p.DefaultRemote + "/" + rev, "refs/tags/" + rev, rev} {
		if sha := resolve(ctx, path, p.CommandPath, ref); sha != "" {
			return sha, nil
		}
	}
	return "", nil
}

func (*Protocol) DateFromRevision(ctx context.Context, a pm.ProtocolArgs, rev string) (time.Time, error) {
	if rev == "" || !ready(a) {
		return time.Time{}, nil
	}
	p := params(a)
	out := firstLine(output(ctx, a.Plugin.String("path"), p.CommandPath, "show", "-s", "--format=%ct", rev))
	sec, err := strconv.ParseInt(out, 10, 64)
	if err != nil {
		return time.Time{}, nil
	}
	return time.Unix(sec, 0), nil
}

func (*Protocol) Histories(ctx context.Context, a pm.ProtocolArgs, start, end string) ([]string, error) {
	if !ready(a) {
		return nil, nil
	}
	args := []string{"rev-list"}
	if start != "" {
		args = append(args, start)
	}
	if end != "" {
		args = append(args, "^"+end)
	}
	var out []string
	for _, l := range strings.Split(output(ctx, a.Plugin.String("path"), params(a).CommandPath, args...), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out, nil
}

func (*Protocol) CheckRemoteCommands(_ context.Context, a pm.ProtocolArgs) ([]pm.Command, error) {
	if !ready(a) {
		return nil, nil
	}
	p := params(a)
	remote := p.DefaultRemote
	return []pm.Command{
		{Command: p.CommandPath, Args: []string{"fetch", remote, "--tags"}},
		{Command: p.CommandPath, Args: []string{"log", "HEAD.." + remote + "/HEAD", "--graph", "--no-show-signature",
			`--pretty=format:"%h [%cr] %s"`}},
	}, nil
}

func defaultBranchName(p pm.Plugin) string {
	if b := attrs(p).GitDefaultBranch; b != "" {
		return b
	}
	return "main"
}

func defaultBranch(ctx context.Context, a pm.ProtocolArgs, p Params) string {
	rev := firstLine(output(ctx, a.Plugin.String("path"), p.CommandPath, "symbolic-ref", "refs/remotes/"+p.DefaultRemote+"/HEAD"))
	if rev == "" || strings.HasPrefix(rev, "fatal: ") {
		return defaultBranchName(a.Plugin)
	}
	// refs/remotes/origin/main: the branch is main.
	return strings.TrimPrefix(rev, "refs/remotes/"+p.DefaultRemote+"/")
}

func resolve(ctx context.Context, dir, git, ref string) string {
	sha := firstLine(output(ctx, dir, git, "rev-parse", "--verify", "--quiet", ref))
	if sha1Re.MatchString(sha) {
		return sha
	}
	return ""
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(l)
}

// output is the standard output of git in dir ("" when it fails).
func output(ctx context.Context, dir, git string, args ...string) string {
	cmd := exec.CommandContext(ctx, git, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}

// gitDir is the git directory of the repository base, or "".
func gitDir(base string) string {
	dotGit := filepath.Join(base, ".git")
	st, err := os.Lstat(dotGit)
	if err != nil {
		return ""
	}
	if st.IsDir() {
		return dotGit
	}
	if st.Mode().IsRegular() {
		b, err := os.ReadFile(dotGit)
		if err != nil {
			return ""
		}
		line := firstLine(string(b))
		if d, ok := strings.CutPrefix(line, "gitdir: "); ok {
			if filepath.IsAbs(d) {
				return d
			}
			return filepath.Join(base, d)
		}
	}
	return ""
}
