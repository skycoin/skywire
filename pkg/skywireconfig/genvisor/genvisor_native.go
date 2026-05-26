//go:build !js

// Package genvisor pkg/skywireconfig/genvisor/genvisor_native.go
//
// Non-WASM implementation of MustMarshalJSON — uses
// encoding/json.MarshalIndent. Build-tag-gated because
// encoding/json's reflect-based marshal calls reflect.unsafe_New /
// mapassign / typedmemmove, which TinyGo's stdlib doesn't ship.
// See the package doc / genvisor_js.go for the WASM alternative.
package genvisor

import (
	"encoding/json"

	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// MustMarshalJSON renders v as indented JSON via the stdlib codec.
func MustMarshalJSON(v *visorconfig.V1) []byte {
	buf, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic("genvisor: V1.MarshalJSON failed: " + err.Error())
	}
	return buf
}
