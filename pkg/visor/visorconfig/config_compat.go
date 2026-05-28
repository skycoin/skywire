// Package visorconfig pkg/visor/visorconfig/config_compat.go —
// backward-compat JSON unmarshaling for renamed config keys.
//
// V1.Pty (canonical) corresponds to operator-config-json's "pty"
// key. The legacy key "dmsgpty" is still accepted on load so that
// existing operator config.json files don't break across the
// rename. Marshaling always emits the canonical "pty" key — V1's
// struct tag is `json:"pty,omitempty"`.
//
// When both keys appear, the canonical "pty" wins. When neither
// appears, V1.Pty stays nil (the visor's init path treats nil as
// "pty subsystem disabled").
package visorconfig

import (
	"encoding/json"
)

// v1Alias avoids unbounded recursion in V1.UnmarshalJSON. Embedding
// V1 directly would call UnmarshalJSON again; aliasing V1 to a
// new type with no methods uses the struct-tag-driven default
// unmarshaler.
type v1Alias V1

// UnmarshalJSON implements json.Unmarshaler for V1. Reads the
// canonical schema via the alias, then patches V1.Pty from the
// legacy "dmsgpty" key if the canonical "pty" key was absent.
func (v *V1) UnmarshalJSON(data []byte) error {
	var alias v1Alias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	*v = V1(alias)

	if v.Pty == nil {
		var legacy struct {
			Dmsgpty *Pty `json:"dmsgpty"`
		}
		if err := json.Unmarshal(data, &legacy); err == nil && legacy.Dmsgpty != nil {
			v.Pty = legacy.Dmsgpty
		}
	}
	return nil
}
