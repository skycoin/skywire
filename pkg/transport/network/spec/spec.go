// Package spec is the WASM-clean schema half of pkg/transport/network.
// Currently holds STCPConfig (the visor-side Skywire-TCP configuration
// stanza) — a pure-data type that pkg/visor/visorconfig.V1 embeds.
//
// Splitting it out of pkg/transport/network lets V1 reference the
// schema directly without transitively pulling in the package's
// operational dependencies (netutil's DefaultNetworkInterface,
// bbolt's transport log store, etc.) which don't compile under
// GOOS=js. pkg/transport/network keeps a `type STCPConfig =
// spec.STCPConfig` alias so existing callers don't need to change
// imports.
package spec

import (
	"github.com/skycoin/skywire/pkg/cipher"
)

// STCPConfig defines config for Skywire-TCP network. Pure data —
// the actual stcp client / listener / dialer live in
// pkg/transport/network and operate on this type.
type STCPConfig struct {
	PKTable          map[cipher.PubKey]string `json:"pk_table"`
	ListeningAddress string                   `json:"listening_address"`
}
