// Package local gives the plugins of a directory, as dpp-ext-local: the
// plugins you develop, used from where they are (they are not installed
// nor updated).
//
//	plugins, err := local.Load(ctx, c.Host, "~/work", pm.Plugin{"merged": false},
//		[]string{"*.vim", "*.nvim"})
package local

import (
	"context"
	"os"
	"path/filepath"

	"github.com/Shougo/goda-pm.vim/pm"
	"github.com/Shougo/goda.vim/sdk"
)

// Load gives a plugin for each directory of directory (expanded by the
// editor: ~ and $VAR) that matches one of includes (all when there are
// none): its name is that of the directory, and it has the options under
// its own.
func Load(ctx context.Context, h sdk.Host, directory string, options pm.Plugin, includes []string) ([]pm.Plugin, error) {
	base, err := pm.Expand(ctx, h, directory)
	if err != nil {
		return nil, err
	}
	if len(includes) == 0 {
		includes = []string{"*"}
	}
	var plugins []pm.Plugin
	for _, include := range includes {
		matches, err := filepath.Glob(filepath.Join(base, include))
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			if st, err := os.Stat(m); err != nil || !st.IsDir() {
				continue
			}
			path := filepath.ToSlash(m)
			plugins = append(plugins, pm.Merge(options, pm.Plugin{
				"repo":  path,
				"local": true,
				"path":  path,
				"name":  filepath.Base(m),
			}))
		}
	}
	return plugins, nil
}
