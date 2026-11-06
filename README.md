# goda-pm.vim

A plugin manager for Vim and Neovim on
[goda.vim](https://github.com/Shougo/goda.vim): the successor of
[dpp.vim](https://github.com/Shougo/dpp.vim), with the configuration in Go
instead of TypeScript, and no Deno.

- The startup does not use the binary: as dpp.vim, goda-pm writes a state
  (a startup script and the plugins) once, and `goda_pm#min#load_state()`
  sources it.
- The TOML files of dpp-ext-toml work as they are (`ext/toml`).
- The protocols `git` and `http` (dpp-protocol-git, dpp-protocol-http; http
  needs no curl, unzip nor tar).
- The ext `installer` (dpp-ext-installer), and the lazy loading
  (dpp-ext-lazy, `ext/lazy`) and the local plugins (dpp-ext-local,
  `ext/local`).
- The source `goda_pm` of [goda-fi.vim](https://github.com/Shougo/goda-fi.vim)
  (ddu-source-dpp), in `ext/fi/pmfi`.
- A Lua entry: `require('goda_pm')`.
- The binaries of the goda.vim plugins are built with the plugins installed
  (`g:goda#workspace` of goda.vim): after an update, build them again, with no
  `go get -u`.

## Requirements

- Vim 9.1 or Neovim 0.11
- Go 1.26
- git
- [goda.vim](https://github.com/Shougo/goda.vim) 0.7 or later (to make the
  state and to install the plugins)

## Usage

The configuration is a Go module (see [examples/config](examples/config)) in
`~/.vim/goda-pm` (Neovim: `stdpath('config') .. '/goda-pm'`): it adds the
protocols and the exts, and gives the plugins.

```go
func main() {
	b := pm.New()
	b.AddProtocol(git.New())
	b.AddExt(installer.New())
	b.SetConfig(func(ctx context.Context, c *pm.ConfigArgs) (*pm.ConfigReturn, error) {
		t, err := toml.Load(ctx, c.Host, "$BASE_DIR/dpp.toml", nil)
		if err != nil {
			return nil, err
		}
		r, err := lazy.MakeState(ctx, c.Host, t.Plugins)
		if err != nil {
			return nil, err
		}
		return &pm.ConfigReturn{Plugins: r.Plugins, StateLines: r.StateLines}, nil
	})
	b.Main()
}
```

Clone goda-pm.vim and goda.vim where the protocol `git` puts them (list them
in your TOML files too, and they are updated as the others):

```sh
git clone https://github.com/Shougo/goda-pm.vim ~/.cache/goda-pm/repos/github.com/Shougo/goda-pm.vim
git clone https://github.com/Shougo/goda.vim ~/.cache/goda-pm/repos/github.com/Shougo/goda.vim
```

In your vimrc:

```vim
" goda-pm.vim must be in 'runtimepath'.
set runtimepath^=~/.cache/goda-pm/repos/github.com/Shougo/goda-pm.vim

const s:base = '~/.cache/goda-pm'
let $BASE_DIR = '<script>:h'->expand()

if s:base->goda_pm#min#load_state()
  " No state: goda.vim is needed to make it.
  set runtimepath^=~/.cache/goda-pm/repos/github.com/Shougo/goda.vim
  autocmd VimEnter * ++once call goda_pm#make_state(s:base)
endif

filetype plugin indent on
syntax on
```

`goda_pm#make_state()` builds the configuration when it is newer than the
binary, and makes the state in the background. Restart Vim, and install the
plugins:

```vim
call goda_pm#async_ext_action('installer', 'install')
```

See `:help goda-pm` for everything else, and `:help goda-pm-dpp` for the
differences from dpp.vim.
