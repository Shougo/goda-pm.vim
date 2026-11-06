// Package toml loads the plugins of a TOML file, as dpp-ext-toml: the same
// files work.
//
//	[[plugins]]
//	repo = 'Shougo/goda-fi.vim'
//	on_cmd = 'GodaFi'
//	hook_add = '''
//	nnoremap ...
//	'''
//
//	[ftplugin]
//	python = '''
//	setlocal ...
//	'''
//
// In the configuration:
//
//	t, err := toml.Load(ctx, c.Host, "$BASE_DIR/dpp.toml", pm.Plugin{"lazy": true})
package toml

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/Shougo/goda-pm.vim/pm"
	"github.com/Shougo/goda.vim/sdk"
	gotoml "github.com/pelletier/go-toml/v2"
)

// Result is what a TOML file has, as dpp-ext-toml's Toml.
type Result struct {
	// Ftplugins are those of [ftplugin].
	Ftplugins map[string]string
	// HooksFile is that of the file (hooks_file).
	HooksFile     string
	MultipleHooks []pm.MultipleHook
	Plugins       []pm.Plugin
}

// Load loads the TOML file path (expanded by the editor: ~ and $VAR):
// each plugin has the options under its own, and its name (the last part of
// its repo by default).
func Load(ctx context.Context, h sdk.Host, file string, options pm.Plugin) (Result, error) {
	full, err := pm.Expand(ctx, h, file)
	if err != nil {
		return Result{}, err
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return Result{}, err
	}
	r, err := Parse(b, options)
	if err != nil {
		return Result{}, fmt.Errorf("%s: parse failed: %w", full, err)
	}
	return r, nil
}

// Parse parses a TOML file.
func Parse(data []byte, options pm.Plugin) (Result, error) {
	var raw map[string]any
	if err := gotoml.Unmarshal(data, &raw); err != nil {
		return Result{}, err
	}
	var r Result
	if v, ok := raw["ftplugin"]; ok {
		if err := sdk.Decode(v, &r.Ftplugins); err != nil {
			return Result{}, fmt.Errorf("ftplugin: %w", err)
		}
	}
	if v, ok := raw["hooks_file"]; ok {
		if err := sdk.Decode(v, &r.HooksFile); err != nil {
			return Result{}, fmt.Errorf("hooks_file: %w", err)
		}
	}
	if v, ok := raw["multiple_hooks"]; ok {
		if err := sdk.Decode(v, &r.MultipleHooks); err != nil {
			return Result{}, fmt.Errorf("multiple_hooks: %w", err)
		}
	}
	if v, ok := raw["plugins"]; ok {
		list, ok := v.([]any)
		if !ok {
			return Result{}, fmt.Errorf("plugins: not an array of tables")
		}
		for i, e := range list {
			m, ok := e.(map[string]any)
			if !ok {
				return Result{}, fmt.Errorf("plugins[%d]: not a table", i)
			}
			p := pm.Merge(options, pm.Plugin(normalize(m).(map[string]any)))
			if _, ok := m["name"]; !ok {
				name := ""
				if repo := strings.TrimRight(p.String("repo"), "/"); repo != "" {
					name = path.Base(repo)
				}
				p["name"] = name
			}
			r.Plugins = append(r.Plugins, p)
		}
	}
	return r, nil
}

// normalize makes the values of the TOML those of JSON (dates become
// strings).
func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = normalize(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalize(e)
		}
		return out
	case string, bool, int64, float64, nil:
		return x
	}
	return fmt.Sprint(v)
}
