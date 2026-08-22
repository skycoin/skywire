// Package api pkg/transport-discovery/api/cxo_register.go c4-net-discovery
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
	if err := api.store.RegisterTransportsBatch(ctx, reporter, []*transport.SignedEntry{sEntry}); err != nil {
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

// ReconcileTransportsFromCXO applies a visor's full transport list published as a
// single snapshot leaf (transports/list). It is the declarative replacement for the
// per-transport entry (register) + tombstone (deregister) leaves: register/refresh
// every entry the reporter is an edge of, and deregister any of the reporter's
// existing transports that are ABSENT from the list (absence = deletion). Idempotent
// and self-healing — a dropped update is corrected by the next snapshot.
func (api *API) ReconcileTransportsFromCXO(ctx context.Context, entries []*transport.Entry, reporter cipher.PubKey, version string) error {
	// Accept only entries the reporter is actually an edge of (auth parity with the
	// per-entry path); build the authoritative keep-set.
	keep := make(map[uuid.UUID]struct{}, len(entries))
	signed := make([]*transport.SignedEntry, 0, len(entries))
	touchedEdges := map[cipher.PubKey]struct{}{}
	for _, e := range entries {
		if e == nil || e.EdgeIndex(reporter) < 0 {
			continue
		}
		keep[e.ID] = struct{}{}
		signed = append(signed, &transport.SignedEntry{Entry: e, Version: version})
		touchedEdges[e.Edges[0]] = struct{}{}
		touchedEdges[e.Edges[1]] = struct{}{}
	}

	// Register/refresh everything currently present.
	if len(signed) > 0 {
		if err := api.store.RegisterTransportsBatch(ctx, reporter, signed); err != nil {
			return fmt.Errorf("register batch: %w", err)
		}
		for _, se := range signed {
			if err := api.store.RecordTransportHeartbeat(ctx, se.Entry.ID, string(se.Entry.Type), time.Time{}); err != nil {
				_ = err //nolint:errcheck // uptime is auxiliary; store logs
			}
		}
	}

	// Deregister any of the reporter's existing transports absent from the snapshot.
	// A transport the reporter no longer lists is a deregister signal for that edge —
	// exactly what a tombstone was in the delta model.
	existing, err := api.store.GetTransportsByEdgeNoLatency(ctx, reporter)
	if err != nil {
		// A reporter with no prior transports in the store (first snapshot, or all
		// aged out) returns ErrTransportNotFound here. That is not a reconcile
		// failure — the register step above already landed this snapshot's
		// entries, and there is simply nothing absent to deregister. Treating it
		// as an error made every fresh/re-announcing visor's reconcile log
		// "ReconcileTransportsFromCXO failed: get existing: transport not found"
		// and return non-nil, which the aggregator surfaces as a fill failure.
		if errors.Is(err, store.ErrTransportNotFound) {
			return nil
		}
		return fmt.Errorf("get existing: %w", err)
	}
	for _, e := range existing {
		if _, ok := keep[e.ID]; ok {
			continue
		}
		if err := api.store.DeregisterTransport(ctx, e.ID); err != nil {
			// Best-effort — a failed absent-deregister self-corrects on the next
			// snapshot; the aggregator logs if the whole reconcile returns an error.
			continue
		}
		touchedEdges[e.Edges[0]] = struct{}{}
		touchedEdges[e.Edges[1]] = struct{}{}
	}

	api.mirrorEdges(ctx, touchedEdges)
	if err := api.store.RecordHeartbeat(ctx, reporter, version); err != nil {
		_ = err //nolint:errcheck // visor-level heartbeat is auxiliary
	}
	return nil
}
