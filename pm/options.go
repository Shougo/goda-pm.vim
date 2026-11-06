package pm

import (
	"encoding/json"
	"fmt"
)

// Params are the params of an ext or a protocol, as the configuration gives
// them. Each decodes them into its own type with DecodeParams.
type Params = map[string]any

// Options are dpp.vim's options: the configuration sets them (ConfigArgs.
// Options), and they are kept in the state for the ext actions.
type Options struct {
	// ExtParams are the params of the exts, by ext name.
	ExtParams map[string]Params `json:"extParams"`
	// HooksFileMarker is the start and the end of a hook in a hooks file.
	HooksFileMarker string `json:"hooksFileMarker"`
	// InlineVimrcs are Vim script (or Lua) files put in the startup script.
	InlineVimrcs []string `json:"inlineVimrcs"`
	// ProtocolParams are the params of the protocols, by protocol name.
	ProtocolParams map[string]Params `json:"protocolParams"`
	// Protocols are the protocols that detect a plugin, in order: all the
	// protocols added, in the order they were, when empty.
	Protocols []string `json:"protocols"`
	// SkipMergeFilenamePattern: the files (a Go regexp) that are not
	// merged.
	SkipMergeFilenamePattern string `json:"skipMergeFilenamePattern"`
}

// DefaultOptions are the options before the configuration changes them.
func DefaultOptions() Options {
	return Options{
		ExtParams:                map[string]Params{},
		HooksFileMarker:          "{{{,}}}",
		InlineVimrcs:             []string{},
		ProtocolParams:           map[string]Params{},
		Protocols:                []string{},
		SkipMergeFilenamePattern: `^tags(?:-\w\w)?$|^package\.json$|\.md$`,
	}
}

// DecodeParams decodes params over dst, a pointer to the params of an ext or
// a protocol that holds their defaults: the params given replace them, the
// others stay. Unknown params are ignored.
func DecodeParams(params Params, dst any) error {
	if len(params) == 0 {
		return nil
	}
	b, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return fmt.Errorf("params: %w", err)
	}
	return nil
}
