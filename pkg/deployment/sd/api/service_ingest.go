// Package api pkg/deployment/sd/api/service_ingest.go c4-net-discovery
//
// SD-registration-over-CXO ingest: the server side of the fan-in path where
// visors publish their live service-entry set as a CXO feed instead of
// re-POSTing every entry over a fresh dmsg stream every 90s (each with a full
// Noise handshake — the secp256k1 handshakeResponder that dominates discovery
// -service CPU). The CXO aggregator (pkg/deployment/sd/regcxo) calls
// IngestServiceFromCXO once per entry in each batch it replicates.
//
// This is purely ADDITIVE and dual-write: the HTTP POST /api/services path
// remains authoritative and keeps writing the same store on the same 90s
// schedule. The CXO ingest therefore never clobbers a fresh HTTP register:
//
//   - When a record already exists (the common case — HTTP registered first),
//     the CXO ingest is a keepalive: it re-writes the STORED entry to refresh
//     its TTL, off a warm CXO connection instead of a fresh handshake. It
//     writes back exactly what is there, so it can never overwrite a newer
//     HTTP registration (mirrors the AR's bind keepalive, which likewise
//     refreshes off the stored record rather than the incoming one).
//   - When no record exists yet, only the non-visor types take a fresh insert.
//     A type=visor registration is gated on ipIsPublic(entry, observed-remote-
//     addr), and the CXO path has no observed remote address to check against
//     — so a fresh visor entry is left to HTTP, exactly as a fresh SUDPH bind
//     is left to the AR's UDP path.
//
// Deregistration is ABSENCE, not a tombstone: a service the visor stops
// publishing simply stops being refreshed here and expires on the store's
// existing entry TTL (Config.EntryTimeout). There is deliberately no CXO
// delete path.
package api

import (
	"context"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/servicedisc"
)

// IngestServiceFromCXO applies one service entry received over the
// SD-registration-over-CXO feed. reporter is the feed's publisher PK (the
// visor); an entry whose address is not reporter's own is DROPPED, so a visor
// can only ever register its own services — the ownership check the HTTP path
// enforces via httpauth is structural here.
//
// See the package doc for the dual-write / no-clobber semantics.
func (a *API) IngestServiceFromCXO(ctx context.Context, reporter cipher.PubKey, se servicedisc.Service) {
	if reporter == (cipher.PubKey{}) || se.Type == "" {
		return
	}
	if se.Addr.PubKey() != reporter {
		a.log.WithField("reporter", reporter).WithField("entry_pk", se.Addr.PubKey()).
			Debug("sd-reg-cxo: entry address is not the publishing visor; dropping")
		return
	}

	// Keepalive-first: if a record already exists, refresh its TTL off the
	// STORED entry. This is the common case and the whole CPU win — a warm
	// CXO Root keeps the registration alive with no fresh Noise handshake.
	// Writing back the stored value can never clobber a newer HTTP register.
	if stored, sErr := a.db.Service(ctx, se.Type, se.Addr); sErr == nil && stored != nil {
		if uErr := a.db.UpdateServiceAndHeartbeat(ctx, stored, stored.Version); uErr != nil {
			a.log.WithError(uErr).WithField("reporter", reporter).WithField("type", se.Type).
				Debug("sd-reg-cxo: TTL keepalive refresh failed")
			return
		}
		a.mirrorVisorServices(ctx, reporter)
		a.cxoPublisher.PutEntry(stored)
		return
	}

	// No record yet. A type=visor entry is admitted by HTTP only after
	// ipIsPublic() matches the observed remote address against the declared
	// LocalIPs; the CXO path observes no address, so it cannot make that
	// judgement — leave a fresh visor registration to HTTP.
	if se.Type == servicedisc.ServiceTypeVisor {
		return
	}
	// Mirror the HTTP handler's version gate for the exit types.
	if (se.Type == servicedisc.ServiceTypeVPN || se.Type == servicedisc.ServiceTypeSkysocks) && se.Version == "" {
		a.log.WithField("reporter", reporter).WithField("type", se.Type).
			Debug("sd-reg-cxo: " + ErrVisorVersionIsTooOld.Error() + "; dropping")
		return
	}

	// Fresh insert. Geo is best-effort enrichment for these types and the
	// HTTP handler already registers them without it when the lookup fails,
	// so an entry that carries none (the visor attaches its own when it has
	// one) is registered as-is.
	entry := se
	if uErr := a.db.UpdateServiceAndHeartbeat(ctx, &entry, entry.Version); uErr != nil {
		a.log.WithError(uErr).WithField("reporter", reporter).WithField("type", entry.Type).
			Debug("sd-reg-cxo: fresh registration failed")
		return
	}
	a.mirrorVisorServices(ctx, reporter)
	a.cxoPublisher.PutEntry(&entry)
	a.log.WithField("reporter", reporter).WithField("type", entry.Type).
		Debug("sd-reg-cxo: fresh registration ingested")
}
