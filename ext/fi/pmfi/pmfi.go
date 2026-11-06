// Package pmfi is the source "goda_pm" of goda-fi.vim
// (https://github.com/Shougo/goda-fi.vim), as ddu-source-dpp: the plugins
// of the state, as items of the kind "file" (their directories), with the
// actions "browse" (open the URL of the plugin) and "update".
//
// Add it in your configuration of goda-fi.vim:
//
//	import "github.com/Shougo/goda-pm.vim/ext/fi/pmfi"
//
//	b.AddSource(pmfi.New())
package pmfi

import (
	"context"
	"slices"
	"strings"

	"github.com/Shougo/goda-fi.vim/ext/kind/file"
	"github.com/Shougo/goda-fi.vim/ext/kind/url"
	"github.com/Shougo/goda-fi.vim/fi"
	"github.com/Shougo/goda-pm.vim/pm"
	"github.com/Shougo/goda.vim/sdk"
)

// Params are the source params (sourceParams.goda_pm).
type Params struct {
	// Names are the plugins (all when empty).
	Names []string `json:"names"`
}

// ActionData is the action of an item: that of the kind "file", and the
// plugin.
type ActionData struct {
	file.ActionData
	URL  string `json:"url"`
	Name string `json:"__name"`
}

// Source is the source "goda_pm".
type Source struct{}

// New returns the source, to add to the builder of goda-fi.
func New() Source { return Source{} }

func (Source) Name() string { return "goda_pm" }
func (Source) Kind() string { return "file" }

// Plugins are the plugins of the state of the editor, sorted by name.
func Plugins(ctx context.Context, h sdk.Host) ([]pm.Plugin, error) {
	v, err := h.Eval(ctx, "exists('g:goda_pm') && has_key(g:goda_pm, 'state') ? g:goda_pm.state.plugins->values() : []")
	if err != nil {
		return nil, err
	}
	var list []map[string]any
	if err := sdk.Decode(v, &list); err != nil {
		return nil, err
	}
	out := make([]pm.Plugin, 0, len(list))
	for _, m := range list {
		out = append(out, pm.Plugin(m))
	}
	slices.SortFunc(out, func(a, b pm.Plugin) int { return strings.Compare(a.Name(), b.Name()) })
	return out, nil
}

func (Source) Gather(ctx context.Context, h sdk.Host, a fi.GatherArgs, emit func([]fi.Item) error) error {
	var p Params
	if err := fi.Decode(a.SourceParams, &p); err != nil {
		return err
	}
	plugins, err := Plugins(ctx, h)
	if err != nil {
		return err
	}
	var items []fi.Item
	for _, pl := range plugins {
		if len(p.Names) > 0 && !slices.Contains(p.Names, pl.Name()) {
			continue
		}
		word := pl.Name()
		if d := pl.String("description"); d != "" {
			word += " : " + d
		}
		items = append(items, fi.Item{
			Word: word,
			Action: ActionData{
				ActionData: file.ActionData{Path: pl.String("path"), IsDirectory: true},
				URL:        pl.String("url"),
				Name:       pl.Name(),
			},
			Info: []fi.Info{{Text: pl.String("path"), HlGroup: "Directory"}},
		})
	}
	return emit(items)
}

func data(items []fi.Item) ([]ActionData, error) {
	out := make([]ActionData, 0, len(items))
	for _, it := range items {
		var d ActionData
		if err := sdk.Decode(it.Action, &d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// Actions are those of ddu-source-dpp.
func (Source) Actions() map[string]fi.Action {
	return map[string]fi.Action{
		"browse": {
			Description: "Browse the plugin URL.",
			Run: func(ctx context.Context, h sdk.Host, a fi.ActionArgs) (fi.ActionResult, error) {
				ds, err := data(a.Items)
				if err != nil {
					return fi.ActionResult{}, err
				}
				for _, d := range ds {
					if d.URL != "" {
						if err := url.SystemOpen(d.URL); err != nil {
							return fi.ActionResult{}, err
						}
					}
				}
				return fi.ActionResult{}, nil
			},
		},
		"update": {
			Description: "Update the plugins.",
			Run: func(ctx context.Context, h sdk.Host, a fi.ActionArgs) (fi.ActionResult, error) {
				ds, err := data(a.Items)
				if err != nil {
					return fi.ActionResult{}, err
				}
				names := make([]string, 0, len(ds))
				for _, d := range ds {
					names = append(names, d.Name)
				}
				return fi.ActionResult{}, h.Notify(ctx, "goda_pm#async_ext_action", "installer", "update",
					map[string]any{"names": names})
			},
		},
	}
}
