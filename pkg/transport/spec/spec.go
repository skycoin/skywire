// Package spec is the WASM-clean schema half of pkg/transport.
// Currently holds PersistentTransports — the wire-format entry the
// visor stores per pinned transport peer. pkg/visor/visorconfig.V1
// embeds a `[]PersistentTransports`, so this type living in a leaf
// package keeps V1 importable from GOOS=js consumers without
// pulling in pkg/transport's operational graph (bbolt log store,
// manager goroutines, etc.).
//
// pkg/transport re-exports `PersistentTransports = spec.PersistentTransports`
// so existing callers compile unchanged.
package spec

import (
	"github.com/skycoin/skywire/pkg/cipher"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// PersistentTransports is a persistent-transport description: the
// pinning entry that tells the visor "always keep a transport open
// to peer PK over network type NetType". Pure data; pkg/transport's
// manager reads these at init and on-config-reload to drive its
// reconciliation loop.
type PersistentTransports struct {
	PK      cipher.PubKey `json:"pk"`
	NetType types.Type    `json:"type"`
}
