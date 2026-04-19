package dht

import (
	"context"
	"encoding/json"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

// EntryMirror mirrors discovery entries to the DHT whenever they are
// written to the centralized store. Call Mirror() after a successful
// store write to asynchronously publish the entry to the DHT.
//
// This is used by deployment services (DMSG discovery, TPD, etc.) to
// ensure entries from old visors (that don't dual-write to DHT) are
// still available in the DHT.
type EntryMirror struct {
	node *Node
	log  *logging.Logger
	salt []byte
}

// NewEntryMirror creates a mirror that publishes entries to the DHT
// under the given salt (e.g., "dmsg", "tp").
func NewEntryMirror(node *Node, salt string, log *logging.Logger) *EntryMirror {
	return &EntryMirror{
		node: node,
		log:  log,
		salt: []byte(salt),
	}
}

// Mirror publishes an entry to the DHT asynchronously. The entry is
// JSON-marshaled and stored under the publisher's PK with the mirror's
// salt. Errors are logged at trace level and never propagated — the
// centralized store is the source of truth.
func (m *EntryMirror) Mirror(entry interface{}, seq uint64) {
	go func() {
		data, err := json.Marshal(entry)
		if err != nil || len(data) > MaxValueSize {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		if err := m.node.Put(ctx, data, seq, m.salt); err != nil {
			m.log.WithError(err).Trace("DHT mirror: publish failed")
		}
	}()
}
