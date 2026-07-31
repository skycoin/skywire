// Package group cmd/apps/skychat/group/key_reconcile.go c4-app-chat
// the session half of key rotation: emitting a keys/<seq> leaf and
// applying one. Split from keyrotate.go for the same reason
// roster_reconcile.go is split from gossip.go — the envelope is testable
// without a transport, this half needs a live publisher.
package group

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
)

// SetKeyChangeHandler installs the callback fired with the converged key
// state after a rotation is applied (or issued), so the Manager can
// persist it. Parallel to SetModChangeHandler.
//
// Persistence here is not decoration: a visor that applied a rotation and
// then restarted without saving it would come back holding only the old
// key — unable to read anything published since, and encrypting into a
// key the rest of the group has retired.
func (s *Session) SetKeyChangeHandler(f func(KeyState)) {
	s.membersMu.Lock()
	s.onKeyChange = f
	s.membersMu.Unlock()
}

// KeyEpoch returns the epoch of the key this session currently encrypts
// with. Zero for a plaintext group or a group still on its create-time
// key.
func (s *Session) KeyEpoch() uint64 {
	s.membersMu.RLock()
	defer s.membersMu.RUnlock()
	return s.cfg.Record.KeyEpoch
}

// encryptionKeyLocked returns the key to seal outgoing bodies with.
// Caller holds membersMu.
func (s *Session) encryptionKeyLocked() []byte {
	return s.cfg.Record.AESKey
}

// decryptionKeys snapshots every key this session can try, current first.
func (s *Session) decryptionKeys() [][]byte {
	s.membersMu.RLock()
	defer s.membersMu.RUnlock()
	return s.cfg.Record.DecryptionKeys()
}

// RotateKeyFor mints a new group key, seals it to each recipient,
// publishes the signed rotation, and installs the key locally. Returns
// the new epoch.
//
// The recipient list is the CALLER's to supply, and that is deliberate.
// The session's cfg.Record.Members is a create-time snapshot that roster
// changes never write back to — the same trap isMember documents — so
// deriving recipients here would seal the key to whoever was in the group
// when the session opened. The Manager holds the authoritative roster (it
// just persisted it) and filters self and banned PKs out.
//
// Ordering matters for the same reason: the caller must have removed the
// evicted PK from the roster BEFORE calling, or the eviction's whole
// purpose is defeated by handing the new key to the peer being cut off.
// Manager.moderate and Manager.RemoveMember both persist the shrunken
// roster first.
//
// A member that is offline right now is still a recipient — its wrap sits
// in the leaf on our feed and it unwraps when it reconnects. No
// re-broadcast is needed: unlike create-time roster state, which leaves
// no leaf behind and therefore needs BroadcastRoster, a rotation IS a
// leaf and stays in the tree.
func (s *Session) RotateKeyFor(recipients []cipher.PubKey) (uint64, error) {
	return s.rotateKeyAt(recipients, time.Now().UTC())
}

func (s *Session) rotateKeyAt(recipients []cipher.PubKey, at time.Time) (uint64, error) {
	if s == nil || s.pub == nil {
		return 0, errors.New("group: RotateKey: no live publisher")
	}
	if !s.cfg.Record.Encrypted() {
		return 0, ErrKeyRotationNotEncrypted
	}
	gid, err := uuid.Parse(s.cfg.Record.ID)
	if err != nil {
		return 0, fmt.Errorf("group: RotateKey: group id %q: %w", s.cfg.Record.ID, err)
	}

	s.membersMu.RLock()
	epoch := s.cfg.Record.KeyEpoch + 1
	// Belt and braces on top of the Manager's filtering: never seal to
	// ourselves (we mint the key) and never to a banned PK, even if the
	// caller's roster hasn't caught up with a ban this session already
	// applied. Handing the new key back to the peer just cut off is the
	// one mistake this whole path exists to prevent.
	filtered := make([]cipher.PubKey, 0, len(recipients))
	for _, pk := range recipients {
		if pk == s.cfg.MyPK || pk == (cipher.PubKey{}) || s.isBannedLocked(pk) {
			continue
		}
		filtered = append(filtered, pk)
	}
	s.membersMu.RUnlock()
	recipients = filtered

	key, err := GenerateAESKey()
	if err != nil {
		return 0, fmt.Errorf("group: RotateKey: %w", err)
	}

	// A group of one: nobody to seal to, so there is no leaf to publish.
	// Still rotate locally — the point of a solo rotation is that the key
	// the departed member held stops being the key we encrypt with.
	if len(recipients) == 0 {
		s.installKeyLocal(epoch, key, at, at)
		s.log.WithField("epoch", epoch).
			Info("group: key rotated with no remaining recipients; new key is local only")
		return epoch, nil
	}

	m, skipped, err := buildKeyMutation(gid, epoch, key, recipients, s.cfg.MySK, at)
	if err != nil {
		return 0, err
	}
	for _, pk := range skipped {
		s.log.WithField("peer", pk.String()).
			Warn("group: RotateKey: could not seal the new key for this member; they will not receive it")
	}
	body, err := MarshalKey(m)
	if err != nil {
		return 0, fmt.Errorf("group: RotateKey: marshal: %w", err)
	}
	seq := s.keySeq.Add(1)
	path := fmt.Sprintf("%s/%05d", KeyPathPrefix, seq)
	if err := s.pub.Put(path, body); err != nil {
		return 0, fmt.Errorf("group: RotateKey: Put %s: %w", path, err)
	}
	// Install only after the leaf is committed. Rotating locally first
	// would leave us encrypting under a key we failed to distribute — the
	// group would keep receiving bodies nobody but us can open.
	s.installKeyLocal(epoch, key, m.IssuedAt, at)
	s.noteLocalMutation(familyKey, cipher.PubKey{}, m.IssuedAt)
	s.log.WithField("epoch", epoch).WithField("recipients", len(m.Wraps)).
		Info("group: rotated group key")
	return epoch, nil
}

// installKeyLocal installs a key into the live session + record and fires
// the persistence callback.
func (s *Session) installKeyLocal(epoch uint64, key []byte, issuedAt, at time.Time) {
	s.membersMu.Lock()
	changed := s.cfg.Record.installKey(epoch, key, issuedAt, at)
	st := keyStateOf(s.cfg.Record)
	f := s.onKeyChange
	s.membersMu.Unlock()
	if !changed {
		return
	}
	// The volume budget belongs to the key, so a new current key starts
	// a new count. Reset only on `changed`: installKey's loser branch
	// (a same-epoch rotation that lost the IssuedAt tiebreak) files the
	// key into the ring without making it current, and zeroing the
	// counter there would let a losing admin's leaf reset the winner's
	// budget and defer the next due rotation indefinitely.
	s.msgsSealed.Store(0)
	if f != nil {
		f(st)
	}
	// A key we just minted can open governance leaves that were parked
	// for want of one — the admin that rotated may have been holding
	// another admin's sealed roster leaf. Cheap when the lot is empty.
	s.drainDeferredGossip()
}

// SetGroupKey pushes key state from the Manager onto a live session —
// used after the Manager rotates or learns a key out of band (the
// admission response). Idempotent.
func (s *Session) SetGroupKey(st KeyState) {
	if len(st.Key) != 32 {
		return
	}
	s.membersMu.Lock()
	st.applyTo(&s.cfg.Record)
	s.membersMu.Unlock()
	// This is the joiner's first key, handed over in the admission
	// response. Governance leaves it saw before that moment are sitting
	// in the parking lot sealed under exactly this key.
	s.drainDeferredGossip()
}

// applyKeyLeaf decodes, authenticates, and applies one keys/<seq> leaf.
//
// Gates, in order, each of which drops the leaf silently:
//
//  1. Signature — otherwise anyone could mint a key.
//  2. Issuer is a CURRENT admin — otherwise a demoted or evicted member
//     could re-key the group and split it in two.
//  3. Freshness by the replay guard — otherwise a replayed rotation
//     rolls the group back onto a key an evicted member still holds,
//     which is precisely the property rotation exists to remove.
//
// Then we look for our own wrap. Not finding one is normal and not an
// error worth shouting about: an admin reads back its own leaf (no
// self-wrap), and a just-evicted PK is deliberately excluded.
func (s *Session) applyKeyLeaf(body []byte) {
	var m KeyMutation
	if err := json.Unmarshal(body, &m); err != nil {
		return
	}
	if m.GroupID.String() != s.cfg.Record.ID {
		return
	}
	if err := VerifyKey(m); err != nil {
		s.log.WithError(err).Debug("group: reconcile: rejecting key rotation with bad signature")
		return
	}

	s.membersMu.Lock()
	if !s.cfg.Record.IsAdmin(m.IssuerPK) {
		s.membersMu.Unlock()
		s.log.WithField("issuer", m.IssuerPK.String()).
			Debug("group: reconcile: rejecting key rotation from non-admin issuer")
		return
	}
	wmKey := watermarkKey(familyKey, cipher.PubKey{})
	if ok, why := mutationFresh(s.mutationSeen, wmKey, m.IssuedAt, time.Now().UTC()); !ok {
		s.membersMu.Unlock()
		s.log.WithField("issuer", m.IssuerPK.String()).WithField("epoch", m.Epoch).
			WithField("reason", why).
			Debug("group: reconcile: rejecting stale key rotation")
		return
	}
	// Blinded leaves address us by tag, which needs our secret key — see
	// wrapForRecipient. Pre-blinding leaves still match by name.
	wrap, mine := m.wrapForRecipient(s.cfg.MySK, s.cfg.MyPK)
	if !mine {
		// Not addressed to us. Do NOT advance the watermark: a later,
		// genuine rotation from another admin at the same instant would
		// then be refused as stale, and we would be stuck on an old key.
		s.membersMu.Unlock()
		s.log.WithField("issuer", m.IssuerPK.String()).WithField("epoch", m.Epoch).
			Debug("group: reconcile: key rotation carries no wrap for us")
		return
	}
	mySK := s.cfg.MySK
	s.membersMu.Unlock()

	key, err := openGroupKey(mySK, wrap.sealerPK(m.IssuerPK), wrap.Sealed)
	if err != nil {
		// A wrap addressed to us that we cannot open is worth a warning,
		// not a debug line: it means we are about to fall behind the
		// group's key and stop being able to read it.
		s.log.WithError(err).WithField("issuer", m.IssuerPK.String()).
			Warn("group: reconcile: could not unwrap the group key addressed to us")
		return
	}

	now := time.Now().UTC()
	s.membersMu.Lock()
	changed := s.cfg.Record.installKey(m.Epoch, key, m.IssuedAt, now)
	s.mutationSeen = recordMutation(s.mutationSeen, wmKey, m.IssuedAt)
	s.cfg.Record.MutationSeen = copyWatermarks(s.mutationSeen)
	seenSnapshot := copyWatermarks(s.mutationSeen)
	st := keyStateOf(s.cfg.Record)
	epoch := s.cfg.Record.KeyEpoch
	f := s.onKeyChange
	s.membersMu.Unlock()

	s.persistWatermarks(seenSnapshot)
	if !changed {
		return
	}
	// A key we adopted from another admin starts its volume budget here,
	// same as one we minted ourselves — see installKeyLocal.
	s.msgsSealed.Store(0)
	if f != nil {
		f(st)
	}
	// Replay whatever we could not read before this key arrived. Sealed
	// governance leaves reach us through a different feed than the
	// rotation that opens them, so arriving first is normal, not an
	// error — and a leaf dropped here would be dropped for good.
	s.drainDeferredGossip()
	s.log.WithField("issuer", m.IssuerPK.String()).WithField("epoch", epoch).
		Info("group: applied rotated group key")
}
