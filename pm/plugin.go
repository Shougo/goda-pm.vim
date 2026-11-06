package pm

import (
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
)

// Plugin is a plugin, as a dictionary of dpp.vim: the keys of a [[plugins]]
// table of a TOML file (repo, name, on_ft, hook_add, ...), and those that
// goda-pm adds (path, rtp, lazy, merged, sourced, ...). It is a map, not a
// struct: the keys are those of the TOML files, and some are found by their
// prefix (hook_*, lua_*, on_*).
//
// The values are those of JSON: string, bool, int64 or float64, []any and
// map[string]any. The methods read a key as the type it is used as.
type Plugin map[string]any

// Name is the name of the plugin.
func (p Plugin) Name() string { return p.String("name") }

// String is the value of key, or "" when it is not a string.
func (p Plugin) String(key string) string {
	s, _ := p[key].(string)
	return s
}

// Bool is the value of key as Vim takes it: false when missing, false or 0.
func (p Plugin) Bool(key string) bool { return Truthy(p[key]) }

// Int is the value of key, or 0.
func (p Plugin) Int(key string) int {
	switch v := p[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, _ := strconv.Atoi(string(v))
		return n
	case bool:
		if v {
			return 1
		}
	}
	return 0
}

// Strings is the value of key as a list, as dpp's convert2List(): a string
// is a list of one.
func (p Plugin) Strings(key string) []string { return ToStrings(p[key]) }

// Has is whether key is set.
func (p Plugin) Has(key string) bool {
	_, ok := p[key]
	return ok
}

// Clone is a shallow copy of p.
func (p Plugin) Clone() Plugin { return maps.Clone(p) }

// Merge is the plugins merged, the later over the earlier ones: { ...a, ...b }
// of dpp.vim. The plugins are not changed.
func Merge(ps ...Plugin) Plugin {
	out := Plugin{}
	for _, p := range ps {
		maps.Copy(out, p)
	}
	return out
}

// ToStrings is v as a list of strings, as dpp's convert2List(): nil is an
// empty list and a string a list of one.
func ToStrings(v any) []string {
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		return []string{x}
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			} else {
				out = append(out, fmt.Sprint(e))
			}
		}
		return out
	}
	return []string{fmt.Sprint(v)}
}

// Truthy is whether Vim takes v as true.
func Truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	case json.Number:
		f, _ := x.Float64()
		return f != 0
	case string:
		// As Vim: a string is a number.
		n, _ := strconv.Atoi(x)
		return n != 0
	}
	return true
}

// Hooks are the hooks of a plugin, without "hook_" (and "lua_").
var Hooks = map[string]bool{
	"add":               true,
	"depends_update":    true,
	"done_update":       true,
	"post_check_update": true,
	"post_source":       true,
	"post_update":       true,
	"pre_update":        true,
	"source":            true,
}

// MultipleHook are hooks of several plugins: hook_add runs when they are all
// available, hook_source and hook_post_source when they are all sourced.
type MultipleHook struct {
	HookAdd        string   `json:"hook_add,omitempty"`
	HooksFile      string   `json:"hooks_file,omitempty"`
	HookPostSource string   `json:"hook_post_source,omitempty"`
	HookSource     string   `json:"hook_source,omitempty"`
	Plugins        []string `json:"plugins"`
}
