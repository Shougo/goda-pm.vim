package pm

import (
	"fmt"
	"regexp"
	"strings"
)

var hookNameRe = regexp.MustCompile(`\s+([0-9a-zA-Z_-]+)\s*`)

// ParseHooksFile parses the lines of a hooks file: the hooks (hook_add,
// lua_source, ...) between the markers (Options.HooksFileMarker, "{{{,}}}"),
// and the ftplugins (the blocks of other names) under "ftplugin":
//
//	" hook_add {{{
//	nnoremap ...
//	" }}}
//	" python {{{
//	setlocal ...
//	" }}}
func ParseHooksFile(marker string, lines []string, path string) (Plugin, error) {
	startMarker, endMarker, _ := strings.Cut(marker, ",")
	options := Plugin{}
	ftplugin := map[string]string{}
	var dest map[string]string
	toOptions := false
	hookName := ""
	nested := 0
	start := 0
	add := func(line string) {
		var cur string
		var ok bool
		if toOptions {
			cur, ok = options[hookName].(string)
		} else if dest != nil {
			cur, ok = dest[hookName]
		} else {
			return
		}
		if ok && cur != "" {
			cur += "\n" + line
		} else {
			cur = line
		}
		if toOptions {
			options[hookName] = cur
		} else {
			dest[hookName] = cur
		}
	}
	for i, line := range lines {
		if hookName == "" {
			pos := strings.LastIndex(line, startMarker)
			if pos < 0 {
				continue
			}
			dest = nil
			toOptions = false
			m := hookNameRe.FindStringSubmatch(line[:pos])
			if m == nil {
				continue
			}
			nested++
			start = i + 1
			hookName = m[1]
			if base, ok := cutHookPrefix(hookName); ok && Hooks[base] {
				toOptions = true
			} else {
				dest = ftplugin
			}
			continue
		}
		if strings.HasSuffix(line, startMarker) {
			nested++
		}
		if strings.HasSuffix(line, endMarker) {
			nested--
			if nested <= 0 {
				hookName = ""
				continue
			}
		}
		add(line)
	}
	if hookName != "" {
		location := ""
		if path != "" {
			location = fmt.Sprintf(" in '%s'", path)
		}
		return nil, fmt.Errorf("unterminated hooks block: %s%s (started at line %d, expected '%s')",
			hookName, location, start, endMarker)
	}
	options["ftplugin"] = ftplugin
	return options, nil
}

func cutHookPrefix(name string) (string, bool) {
	if s, ok := strings.CutPrefix(name, "hook_"); ok {
		return s, true
	}
	return strings.CutPrefix(name, "lua_")
}

// MergeFtplugins merges the ftplugins src into dest: the keys are
// filetypes joined with "_" ("c_cpp"), "_" (after every ftplugin), and those
// of Lua start with "lua_".
func MergeFtplugins(dest, src map[string]string) {
	for filetype, code := range src {
		plugin := code
		if strings.HasPrefix(filetype, "lua_") {
			plugin = "lua <<EOF\n" + code + "\nEOF\n"
		}
		if filetype == "_" {
			dest[filetype] = plugin
			continue
		}
		for _, ft := range strings.Split(strings.TrimPrefix(filetype, "lua_"), "_") {
			if cur, ok := dest[ft]; ok && cur != "" {
				dest[ft] = cur + "\n" + plugin
			} else {
				dest[ft] = plugin
			}
		}
	}
}

// ftpluginOf is the "ftplugin" of a plugin as a map of strings.
func ftpluginOf(v any) map[string]string {
	out := map[string]string{}
	switch m := v.(type) {
	case map[string]string:
		for k, s := range m {
			out[k] = s
		}
	case map[string]any:
		for k, s := range m {
			if str, ok := s.(string); ok {
				out[k] = str
			}
		}
	case Plugin:
		return ftpluginOf(map[string]any(m))
	}
	return out
}
