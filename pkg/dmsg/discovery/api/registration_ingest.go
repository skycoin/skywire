// Package api pkg/dmsg/discovery/api/registration_ingest.go c1-net-dmsg
//
// Registration-over-CXO ingest: the server side of the fan-in path where
// visors publish their own signed discovery entry as a CXO feed instead
// of re-PUTting it over HTTP on a timer (each PUT a fresh Noise + PQ
// handshake — the load that dominates dmsg-discovery CPU). The CXO
// aggregator (pkg/dmsg/discovery/regcxo) calls IngestEntryFromCXO for
// each entry it replicates.
package api

import (
	"context"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

// IngestEntryFromCXO applies a client discovery entry received over the
// registration-over-CXO feed. reporter is the feed's publisher PK (the
// visor); it MUST equal entry.Static since a visor may only publish its
// own entry.
//
// It mirrors the validation and side effects of the HTTP setEntry handler
// (Validate, VerifySignature, sequence-iteration guard, SetEntry, DHT
// mirror, clients-by-server CXO publish, heartbeat) with ONE difference:
// a stale-or-equal sequence is a silent idempotent no-op rather than an
// error. During the migration a visor dual-writes the same entry over
// HTTP and CXO, so whichever path lands first wins and the other simply
// finds the store already at that sequence. Keep this in sync with
// (*API).setEntry — deliberately duplicated (not refactored out of the
// hot HTTP path) to keep this additive, opt-in path from touching
// production registration behaviour.
func (a *API) IngestEntryFromCXO(ctx context.Context, entry *disc.Entry, reporter cipher.PubKey) {
	if entry == nil {
		return
	}
	// Registration-over-CXO is for client (visor) entries only. Server
	// entries register over HTTP; a client must not be able to publish a
	// server entry (which advertises a reachable address) via its own feed.
	if entry.Client == nil || entry.Server != nil {
		log.WithField("reporter", reporter).Debug("registration-cxo: ignoring non-client entry")
		return
	}
	// A visor may only publish its OWN entry: the feed PK (reporter) signs
	// the Root and entry.Static is what the entry signature covers.
	// Requiring them equal stops a visor from registering another PK.
	if entry.Static != reporter {
		log.WithField("reporter", reporter).WithField("entry_pk", entry.Static).
			Warn("registration-cxo: entry.Static != feed PK; dropping")
		return
	}
	// Client entries skip timestamp validation (they no longer refresh on a
	// timer), matching setEntry's validateTimestamp=false for clients.
	if err := entry.Validate(false); err != nil {
		log.WithError(err).WithField("reporter", reporter).Debug("registration-cxo: entry validation failed")
		return
	}
	if err := entry.VerifySignature(); err != nil {
		log.WithField("reporter", reporter).Debug("registration-cxo: signature verification failed")
		return
	}

	old, err := a.db.Entry(ctx, entry.Static)
	if err == disc.ErrKeyNotFound {
		if e := a.db.SetEntry(ctx, entry, a.clientEntryTTL); e != nil {
			log.WithError(e).WithField("reporter", reporter).Debug("registration-cxo: SetEntry (insert) failed")
			return
		}
		a.afterEntrySetFromCXO(ctx, nil, entry)
		return
	}
	if err != nil {
		log.WithError(err).WithField("reporter", reporter).Debug("registration-cxo: store lookup failed")
		return
	}
	// Idempotent: we already hold this (or a newer) entry — the HTTP path or
	// an earlier Root already applied it. Not an error.
	if entry.Sequence <= old.Sequence {
		return
	}
	if err := old.ValidateIteration(entry); err != nil {
		// A sequence gap or non-monotonic timestamp — let the HTTP path,
		// which has the read-modify-write retry, own the reconciliation.
		log.WithError(err).WithField("reporter", reporter).Debug("registration-cxo: iteration invalid; deferring to HTTP")
		return
	}
	if e := a.db.SetEntry(ctx, entry, a.clientEntryTTL); e != nil {
		log.WithError(e).WithField("reporter", reporter).Debug("registration-cxo: SetEntry (update) failed")
		return
	}
	a.afterEntrySetFromCXO(ctx, old, entry)
	log.WithField("reporter", reporter).WithField("sequence", entry.Sequence).
		Debug("registration-cxo: entry ingested")
}

// afterEntrySetFromCXO fans the post-store side effects that setEntry runs
// inline: DHT mirror, clients-by-server CXO fan-out, and the uptime
// heartbeat. All are best-effort and nil-safe.
func (a *API) afterEntrySetFromCXO(ctx context.Context, old, entry *disc.Entry) {
	if a.dhtMirror != nil {
		a.dhtMirror.Mirror(entry.Static, entry, entry.Sequence)
	}
	a.cxoPublisher.PublishSetEntry(old, entry)
	_ = a.db.RecordHeartbeat(ctx, entry.Static, entry.ClientType) //nolint:errcheck
}
