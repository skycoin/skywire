//go:build !tinygo || (js && wasm)

// Package disc pkg/dmsg/disc/cxo_entry.go c1-net-dmsg
//
// Build-tag-gated off the WASM path like fallback.go — it imports
// pkg/logging (logrus → encoding/json). Constructed only from
// pkg/dmsgc at visor runtime.
//
// cxoEntryClient wraps an APIClient and answers Entry() for peer
// CLIENT lookups from a locally-held CXO index FIRST, falling back to
// the wrapped client (HTTP-over-dmsg) on a miss. Every other method
// passes straight through.
//
// This exists to kill the per-lookup dmsg Noise + post-quantum
// handshake that dominates dmsg-discovery CPU (profiled 76% CPU in
// GC from handshake crypto): each HTTP-over-dmsg Entry() lookup opens
// a fresh session with a full handshake. The visor already ingests
// dmsg-discovery's clients-by-server CXO feed into a snapshot for the
// network visualizer; this reuses that snapshot as a resolution path
// so a peer entry that's already in the local index is served with no
// network round-trip at all.
//
// Back-compat contract (see the deployment-services-over-CXO
// roadmap): dual-path, HTTP stays the source of truth. A resolver
// MISS (peer not in the local index, or an index that hasn't synced
// yet) falls through to the wrapped client — "not in my CXO tree" is
// NEVER treated as "doesn't exist". The resolver only answers when it
// holds a usable client entry (Client != nil with a non-empty
// DelegatedServers list); server-entry lookups and the visor's own
// registration read-back always miss here and take the HTTP path.
package disc

import (
	"context"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// EntryResolveFunc resolves a peer's discovery entry from a local,
// non-network source (the CXO clients-by-server snapshot). It returns
// ok=false to signal a miss, in which case the caller falls back to
// the HTTP discovery client. Implementations MUST return a usable
// client entry (Entry.Client != nil) on a hit, or ok=false; a partial
// or malformed entry must be reported as a miss so the HTTP path can
// resolve it authoritatively.
type EntryResolveFunc func(context.Context, cipher.PubKey) (*Entry, bool)

// cxoEntryClient decorates an APIClient so peer client-entry lookups
// consult a CXO-backed resolver before the wrapped HTTP client.
type cxoEntryClient struct {
	APIClient
	resolve EntryResolveFunc
	log     *logging.Logger
}

// NewCXOEntryClient wraps primary so Entry() consults resolve first,
// falling back to primary on a miss. resolve may be nil (the wrapper
// then behaves exactly like primary). primary must be non-nil.
func NewCXOEntryClient(primary APIClient, resolve EntryResolveFunc, log *logging.Logger) APIClient {
	if resolve == nil {
		return primary
	}
	return &cxoEntryClient{APIClient: primary, resolve: resolve, log: log}
}

// Entry resolves pk's discovery entry from the CXO index first,
// falling back to the wrapped client on a miss. Only client entries
// with a non-empty delegated-server list are served from CXO; every
// other case (miss, server entry, empty delegation) delegates to the
// wrapped client so the HTTP path stays authoritative.
func (c *cxoEntryClient) Entry(ctx context.Context, pk cipher.PubKey) (*Entry, error) {
	if entry, ok := c.resolve(ctx, pk); ok && entry != nil && entry.Client != nil && len(entry.Client.DelegatedServers) > 0 {
		if c.log != nil {
			c.log.WithField("client", pk.String()).Debug("Resolved dmsg entry from CXO index (no HTTP handshake)")
		}
		return entry, nil
	}
	return c.APIClient.Entry(ctx, pk)
}
