// Package api pkg/transport-discovery/api/cxo_register.go
//
// CXO-driven register / deregister entry points. The visor publishes
// transports/<uuid>/entry and transports/<uuid>/tombstone leaves on
// its TreeStore feed; pkg/transport-discovery/cxoaggregator dispatches
// those leaves to these methods on the API.
//
// The auth check (reporter must be an edge of the entry / existing
// entry) is duplicated here even though CXO already authenticates the
// publishing visor by Root signature: the Root sig proves the visor
// PK that signed it, but the leaf itself can carry any payload, so
// TPD has to reject entries that name an unrelated PK as an edge.
// This is the parity check for the SW-Sig httpauth used by the v2/v3
// HTTP register endpoints, where the "auth PK is an edge" filter is
// applied during registerTransportV3 (see endpoints.go).
package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport-discovery/store"
)

// RegisterTransportFromCXO applies a transport entry published by a
// visor on its TreeStore feed. Mirrors the work the v3 HTTP register
// handler does: store write, per-entry heartbeat, mirrorEdges, and
// per-visor heartbeat. Returns an error on auth failure or store
// failure; the aggregator logs it at debug.
func (api *API) RegisterTransportFromCXO(ctx context.Context, entry *transport.Entry, reporter cipher.PubKey, version string) error {
	if entry == nil {
		return errors.New("nil entry")
	}
	if entry.EdgeIndex(reporter) < 0 {
		return fmt.Errorf("reporter %s is not an edge of entry %s", reporter, entry.ID)
	}

	sEntry := &transport.SignedEntry{Entry: entry, Version: version}
	if err := api.store.RegisterTransportsBatch(ctx, []*transport.SignedEntry{sEntry}); err != nil {
		return fmt.Errorf("register batch: %w", err)
	}

	// Heartbeat into the per-transport uptime tables. Pre-uptime
	// visors won't carry a usable Type and the store's
	// RecordTransportHeartbeat early-returns on the type filter; we
	// still call it so a v3-shaped entry from a current visor sets
	// today's slot bit on register tick (the same behavior the HTTP
	// v3 handler has at endpoints.go:94).
	if err := api.store.RecordTransportHeartbeat(ctx, entry.ID, string(entry.Type), time.Time{}); err != nil {
		// Best-effort — uptime is auxiliary and the store layer logs.
		_ = err //nolint:errcheck
	}

	touchedEdges := map[cipher.PubKey]struct{}{
		entry.Edges[0]: {},
		entry.Edges[1]: {},
	}
	api.mirrorEdges(ctx, touchedEdges)

	if err := api.store.RecordHeartbeat(ctx, reporter, version); err != nil {
		// Best-effort — visor-level heartbeat is auxiliary.
		_ = err //nolint:errcheck
	}
	return nil
}

// DeregisterTransportFromCXO applies a tombstone published by a visor
// on its TreeStore feed. An unknown ID is a no-op (idempotent — a
// tombstone that arrives after TPD-side eviction is normal).
func (api *API) DeregisterTransportFromCXO(ctx context.Context, id uuid.UUID, reporter cipher.PubKey) error {
	existing, err := api.store.GetTransportByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrTransportNotFound) {
			return nil
		}
		return fmt.Errorf("lookup existing: %w", err)
	}
	if existing.EdgeIndex(reporter) < 0 {
		return fmt.Errorf("reporter %s is not an edge of existing entry %s", reporter, id)
	}

	if err := api.store.DeregisterTransport(ctx, id); err != nil {
		return fmt.Errorf("deregister: %w", err)
	}

	touchedEdges := map[cipher.PubKey]struct{}{
		existing.Edges[0]: {},
		existing.Edges[1]: {},
	}
	api.mirrorEdges(ctx, touchedEdges)
	return nil
}
