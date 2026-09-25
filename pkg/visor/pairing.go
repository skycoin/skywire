// Package visor pkg/visor/pairing.go c3-vis-core
//
// Visor-level wrapper around the cmd/apps/skychat/pairing package.
//
// The visor owns one pairing.Manager (initialized in init_pairing.go)
// and an in-memory ring of recently-received messages. RPC clients
// call PairAdd / PairRemove / PairList to manage their contact list,
// PairSend to dispatch a message into the corresponding pair feed,
// and PairPoll to drain inbound messages. The poll model is
// intentionally simple — long-poll / SSE delivery to apps can layer
// on later (PR-4) without changing the Visor surface.
package visor

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/skycoin/skywire/cmd/apps/skychat/pairing"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// pairConnectTimeout bounds the dmsg dial in PairAdd. PairAdd is
// operator-initiated (HTTP from the chat-app), so a single
// unreachable peer shouldn't block the HTTP request beyond this
// window. The publisher side is up regardless; the subscriber side
// reconnects on next Resume.
const pairConnectTimeout = 15 * time.Second

// ErrPairingDisabled is returned by Visor pair methods when the
// pairing manager isn't initialized (dmsg unavailable at startup,
// or the bolt store failed to open).
var ErrPairingDisabled = errors.New("pairing: manager not initialized")

// defaultInboxCap is the bounded ring size for pair messages.
// Sized for "few hundred messages a minute under burst" without
// unbounded growth if the consumer is slow or absent.
const defaultInboxCap = 1024

// PairAdd creates a pair record for peerPK, brings up the local
// publisher (allowlist=[peerPK]) and a subscriber dialing peerPK at
// the deterministic pair-publisher port. The peer must independently
// call PairAdd on its own visor for the connection to actually
// succeed; until then the subscriber sits idle.
//
// Idempotent — calling PairAdd for an existing peer returns nil.
func (v *Visor) PairAdd(peerPK cipher.PubKey) error {
	mgr := v.pairManager()
	if mgr == nil {
		return ErrPairingDisabled
	}
	p, err := mgr.Add(peerPK)
	if err != nil {
		return err
	}
	// Bound the dial: PairAdd is operator-initiated (HTTP from the
	// chat-app), so a single unreachable peer shouldn't block the
	// HTTP request beyond the per-attempt window. Subscriber.Connect's
	// ctx is plumbed all the way to dmsg.Client.Dial post-T2a.
	dctx, dcancel := context.WithTimeout(context.Background(), pairConnectTimeout)
	defer dcancel()
	if err := p.Connect(dctx); err != nil {
		// Connect-failure is non-fatal — the publisher side is up,
		// and Resume on a future restart (or a manual retry) will
		// pick up the subscriber side once the peer is reachable.
		v.log.WithError(err).
			WithField("peer", peerPK.Hex()).
			Debug("Pairing: subscriber Connect failed; pair record kept")
	}
	return nil
}

// PairList returns a snapshot of every persisted pair (pending /
// active / revoked).
func (v *Visor) PairList() ([]visorapi.PairInfo, error) {
	v.initLock.RLock()
	store := v.pairing.store
	v.initLock.RUnlock()
	if store == nil {
		return nil, ErrPairingDisabled
	}
	records, err := store.List()
	if err != nil {
		return nil, err
	}
	mgr := v.pairManager()
	out := make([]visorapi.PairInfo, 0, len(records))
	for _, r := range records {
		info := visorapi.PairInfo{
			PeerPK:        r.PeerPK,
			Status:        r.Status,
			Port:          r.Port,
			EstablishedAt: r.EstablishedAt,
			LastMessageAt: r.LastMessageAt,
		}
		// Epoch state comes from the LIVE pair, not the record: the
		// record's ratchet is the last persisted snapshot, and the pair
		// may have advanced since. A revoked or not-yet-resumed pair
		// simply reports no epoch.
		if r.Ratchet != nil {
			info.KeyGeneration = r.Ratchet.Generation
		}
		if mgr != nil {
			if p, live := mgr.Get(r.PeerPK); live {
				if id, ok := p.EpochID(); ok {
					info.Epoch = id.String()
					info.ForwardSecret = true
				}
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].EstablishedAt.Before(out[j].EstablishedAt)
	})
	return out, nil
}

// PairRemove tears down the pair (closing publisher + subscriber)
// and marks the record revoked. The bbolt record is preserved for
// audit; future tooling may add hard-purge.
func (v *Visor) PairRemove(peerPK cipher.PubKey) error {
	mgr := v.pairManager()
	if mgr == nil {
		return ErrPairingDisabled
	}
	return mgr.Remove(peerPK)
}

// PairMarkActive transitions a previously-added pair from
// pending → active. Called by the initiator's skychat when the
// peer's pair-ack arrives, signaling that both sides have
// publishers up and the user-visible status should reflect a
// confirmed pair.
//
// No-op if the record is already active. Returns an error when
// pairing is disabled or no record exists for peerPK.
func (v *Visor) PairMarkActive(peerPK cipher.PubKey) error {
	mgr := v.pairManager()
	if mgr == nil {
		return ErrPairingDisabled
	}
	return mgr.MarkActive(peerPK)
}

// PairSend publishes one message to peerPK's pair feed and returns the
// new message's id — the same id the peer will see on it, so the caller
// can later retract it with PairDelete. Returns ErrNotFound if peerPK
// isn't a registered pair.
func (v *Visor) PairSend(peerPK cipher.PubKey, text string) (string, error) {
	mgr := v.pairManager()
	if mgr == nil {
		return "", ErrPairingDisabled
	}
	p, ok := mgr.Get(peerPK)
	if !ok {
		return "", errors.New("pairing: no live pair for peer")
	}
	id, err := p.SendID(text)
	if err != nil {
		return "", err
	}
	v.initLock.RLock()
	store := v.pairing.store
	v.initLock.RUnlock()
	if store != nil {
		_ = store.MarkMessage(peerPK, time.Now().UTC()) //nolint:errcheck
	}
	return id, nil
}

// PairDelete retracts a message we previously sent to peerPK, naming it
// by the id PairSend returned. The retraction is published onto the pair
// feed, so it reaches the peer even if they are offline right now —
// delete-for-everyone over CXO, matching what a chat-delete envelope does
// over a framed DM conn.
func (v *Visor) PairDelete(peerPK cipher.PubKey, id string) error {
	mgr := v.pairManager()
	if mgr == nil {
		return ErrPairingDisabled
	}
	p, ok := mgr.Get(peerPK)
	if !ok {
		return errors.New("pairing: no live pair for peer")
	}
	return p.SendDelete(id)
}

// PairPoll returns inbound pair messages with TS strictly after
// `since`. Pass time.Time{} (zero) to retrieve the entire current
// inbox window. Messages older than the inbox capacity are dropped
// silently — clients that want guaranteed delivery should poll
// often enough that the window doesn't roll over between polls.
func (v *Visor) PairPoll(since time.Time) ([]visorapi.PairMessage, error) {
	v.initLock.RLock()
	inbox := v.pairing.inbox
	v.initLock.RUnlock()
	if inbox == nil {
		return nil, ErrPairingDisabled
	}
	return inbox.snapshotAfter(since), nil
}

func (v *Visor) pairManager() *pairing.Manager {
	v.initLock.RLock()
	defer v.initLock.RUnlock()
	return v.pairing.manager
}

// pairInbox is a bounded ring buffer of inbound pair messages,
// drained by PairPoll. Single mutex; all operations are O(n) over
// the current ring contents on snapshot, which is fine at
// defaultInboxCap.
type pairInbox struct {
	mu  sync.Mutex
	cap int
	buf []visorapi.PairMessage
}

func newPairInbox(capacity int) *pairInbox {
	if capacity <= 0 {
		capacity = defaultInboxCap
	}
	return &pairInbox{cap: capacity, buf: make([]visorapi.PairMessage, 0, capacity)}
}

func (p *pairInbox) deliver(peerPK cipher.PubKey, msg pairing.Message) {
	p.mu.Lock()
	defer p.mu.Unlock()
	// A chat message is named by its own id; a delete record carries the
	// id of its target instead, which is the whole point of the record.
	id := msg.ID
	if msg.Type == "" {
		id = msg.MsgID()
	}
	p.buf = append(p.buf, visorapi.PairMessage{
		PeerPK: peerPK,
		Text:   msg.Text,
		TS:     msg.TS,
		ID:     id,
		Type:   msg.Type,
	})
	if len(p.buf) > p.cap {
		// Drop the oldest entries to fit the cap. Slice copy keeps
		// the backing array bounded.
		drop := len(p.buf) - p.cap
		p.buf = append(p.buf[:0], p.buf[drop:]...)
	}
}

func (p *pairInbox) snapshotAfter(since time.Time) []visorapi.PairMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]visorapi.PairMessage, 0, len(p.buf))
	for _, m := range p.buf {
		if m.TS.After(since) {
			out = append(out, m)
		}
	}
	return out
}
