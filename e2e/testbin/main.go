// Command testbin is the binary of the end-to-end tests: a configuration of
// the TOML file $GODA_PM_TOML and the local plugins of $GODA_PM_LOCAL.
package main

import (
	"context"
	"os"

	"github.com/Shougo/goda-pm.vim/ext/installer"
	"github.com/Shougo/goda-pm.vim/ext/lazy"
	"github.com/Shougo/goda-pm.vim/ext/local"
	"github.com/Shougo/goda-pm.vim/ext/protocol/git"
	"github.com/Shougo/goda-pm.vim/ext/protocol/http"
	"github.com/Shougo/goda-pm.vim/ext/toml"
	"github.com/Shougo/goda-pm.vim/pm"
)

func main() {
	b := pm.New()
	b.AddProtocol(git.New())
	b.AddProtocol(http.New())
	b.AddExt(installer.New())
	b.SetConfig(config)
	b.Main()
}

func config(ctx context.Context, c *pm.ConfigArgs) (*pm.ConfigReturn, error) {
	c.Options.InlineVimrcs = []string{"$GODA_PM_FILES/inline.vim"}
	c.Options.Protocols = []string{"git", "http"}
	c.Options.ExtParams["installer"] = pm.Params{"logFilePath": "$GODA_PM_FILES/installer.log"}

	plugins := map[string]pm.Plugin{}
	var order []string
	add := func(p pm.Plugin) {
		if _, ok := plugins[p.Name()]; !ok {
			order = append(order, p.Name())
		}
		plugins[p.Name()] = p
	}
	ret := &pm.ConfigReturn{Ftplugins: map[string]string{}}
	for _, f := range []struct {
		path    string
		options pm.Plugin
	}{
		{"$GODA_PM_FILES/plugins.toml", pm.Plugin{"lazy": false}},
		{"$GODA_PM_FILES/lazy.toml", pm.Plugin{"lazy": true}},
	} {
		t, err := toml.Load(ctx, c.Host, f.path, f.options)
		if err != nil {
			return nil, err
		}
		for _, p := range t.Plugins {
			add(p)
		}
		pm.MergeFtplugins(ret.Ftplugins, t.Ftplugins)
		ret.MultipleHooks = append(ret.MultipleHooks, t.MultipleHooks...)
		if t.HooksFile != "" {
			ret.HooksFiles = append(ret.HooksFiles, t.HooksFile)
		}
	}

	locals, err := local.Load(ctx, c.Host, os.Getenv("GODA_PM_LOCAL"), pm.Plugin{"merged": false}, []string{"*.vim"})
	if err != nil {
		return nil, err
	}
	for _, p := range locals {
		if old, ok := plugins[p.Name()]; ok {
			url, err := c.URL(ctx, "git", old)
			if err != nil {
				return nil, err
			}
			p = pm.Merge(old, p, pm.Plugin{"url": url})
		}
		add(p)
	}

	var list []pm.Plugin
	for _, n := range order {
		list = append(list, plugins[n])
	}
	r, err := lazy.MakeState(ctx, c.Host, list)
	if err != nil {
		return nil, err
	}
	ret.Plugins = r.Plugins
	ret.StateLines = r.StateLines
	ret.CheckFiles = []string{"$GODA_PM_FILES/plugins.toml", "$GODA_PM_FILES/lazy.toml"}
	return ret, nil
}
