package dht

import (
	"context"
	"crypto/sha256"
	"encoding/json"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// EntryMirror publishes discovery entries to the DHT on behalf of
// visors that don't dual-write. The mirror signs the DHT item with
// its own key (the deployment service's key) but stores it under
// the visor's target key so lookups by visor PK find it.
//
// The DHT store accepts these items because the mirror's PK is in
// the trust policy's whitelisted or trusted set, and PutMirror
// explicitly allows the signer PK to differ from the target PK.
type EntryMirror struct {
	node *Node
	log  *logging.Logger
	salt []byte
}

// NewEntryMirror creates a mirror for the given salt (e.g., "dmsg", "tp").
func NewEntryMirror(node *Node, salt string, log *logging.Logger) *EntryMirror {
	return &EntryMirror{
		node: node,
		log:  log,
		salt: []byte(salt),
	}
}

// Mirror publishes a discovery entry to the DHT under the subject
// visor's PK. The item is signed by the mirror node's own key.
//
// subjectPK is the visor whose entry this is (determines the DHT
// target for lookups).
func (m *EntryMirror) Mirror(subjectPK cipher.PubKey, entry interface{}, seq uint64) {
	m.MirrorMany([]cipher.PubKey{subjectPK}, entry, seq)
}

// MirrorMany publishes a discovery entry to the DHT under multiple
// subject PKs' targets. The signed payload depends only on seq, value,
// and salt — not the target — so we sign once and push under each
// target. Useful for transports which have multiple edges.
func (m *EntryMirror) MirrorMany(subjectPKs []cipher.PubKey, entry interface{}, seq uint64) {
	if len(subjectPKs) == 0 {
		return
	}
	pks := make([]cipher.PubKey, len(subjectPKs))
	copy(pks, subjectPKs)
	go func() {
		data, err := json.Marshal(entry)
		if err != nil {
			m.log.WithError(err).
				WithField("salt", string(m.salt)).
				WithField("subjects", len(pks)).
				Warn("DHT mirror: marshal failed, dropping publish")
			return
		}
		if len(data) > MaxValueSize {
			// Silently dropping a publish silently corrupts the DHT
			// view — log it so size-induced data loss is visible.
			// (Production hit this with hub-edge transport lists at
			// ~16 KB before MaxValueSize was raised to 64 KB.)
			m.log.WithField("salt", string(m.salt)).
				WithField("subjects", len(pks)).
				WithField("size", len(data)).
				WithField("max", MaxValueSize).
				Warn("DHT mirror: payload exceeds MaxValueSize, dropping publish")
			return
		}

		// Build item signed by the mirror node, but with a target
		// derived from each subject PK (not the mirror's PK).
		item := MutableItem{
			K:    m.node.pk, // signed by mirror node
			Seq:  seq,
			V:    data,
			Salt: m.salt,
		}
		if err := item.Sign(m.node.sk); err != nil {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()

		for _, pk := range pks {
			target := mirrorTarget(pk, m.salt)

			// Store locally under the subject's target.
			m.node.store.PutMirror(target, item)

			// Push to K closest nodes for the subject's target.
			closest, _, _ := m.node.iterativeLookup(ctx, target, false) //nolint:errcheck
			for _, p := range closest {
				_ = m.node.rpcPutMirror(ctx, p, item, target) //nolint:errcheck
			}
		}
	}()
}

// Delete removes the DHT target for the given subject PK from the
// local store. Used when a mirrored entry is deleted from the
// source-of-truth store so the DHT view doesn't carry a stale record
// indefinitely. The deletion propagates organically as other full
// nodes sync and observe the absence.
func (m *EntryMirror) Delete(subjectPK cipher.PubKey) {
	go func() {
		target := mirrorTarget(subjectPK, m.salt)
		m.node.store.Delete(target)
	}()
}

// mirrorTarget computes SHA256(subjectPK || salt) — the same target
// that the visor itself would use if it published directly.
func mirrorTarget(pk cipher.PubKey, salt []byte) NodeID {
	h := sha256.New()
	h.Write(pk[:])
	h.Write(salt)
	var id NodeID
	copy(id[:], h.Sum(nil))
	return id
}
