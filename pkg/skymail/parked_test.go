// Package skymail pkg/skymail/parked_test.go c3-app-mail
package skymail

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// parkStore stands in for the recipients' dmsg-async inbox hosts: one host
// that holds for every key.
type parkStore struct {
	mu    sync.Mutex
	host  cipher.PubKey
	next  int
	msgs  map[cipher.PubKey][]Parked
	refs  map[string]bool
	err   error
	calls int
}

func newParkStore() *parkStore {
	h, _ := cipher.GenerateKeyPair()
	return &parkStore{host: h, msgs: map[cipher.PubKey][]Parked{}, refs: map[string]bool{}}
}

func (s *parkStore) count(pk cipher.PubKey) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.msgs[pk])
}

// parker is one visor's view of the store.
type parker struct {
	s    *parkStore
	self cipher.PubKey
}

func (p parker) Park(_ context.Context, rcpt cipher.PubKey, env []byte, ref string) (cipher.PubKey, error) {
	p.s.mu.Lock()
	defer p.s.mu.Unlock()
	p.s.calls++
	if p.s.err != nil {
		return cipher.PubKey{}, p.s.err
	}
	key := p.self.Hex() + "/" + ref
	if !p.s.refs[key] {
		p.s.refs[key] = true
		p.s.next++
		p.s.msgs[rcpt] = append(p.s.msgs[rcpt], Parked{ID: fmt.Sprint(p.s.next), Host: p.s.host, From: p.self, Body: env})
	}
	return p.s.host, nil
}

func (p parker) Unpark(context.Context) ([]Parked, error) {
	p.s.mu.Lock()
	defer p.s.mu.Unlock()
	return append([]Parked(nil), p.s.msgs[p.self]...), nil
}

func (p parker) Ack(_ context.Context, done []Parked) error {
	p.s.mu.Lock()
	defer p.s.mu.Unlock()
	keep := p.s.msgs[p.self][:0]
	for _, m := range p.s.msgs[p.self] {
		acked := false
		for _, d := range done {
			acked = acked || d.ID == m.ID
		}
		if !acked {
			keep = append(keep, m)
		}
	}
	p.s.msgs[p.self] = keep
	return nil
}

// setParker gives an open test visor the store as its inbox hosts.
func setParker(v *visor, s *parkStore) {
	v.mb.cfg.Parker = parker{s: s, self: v.pk}
	if v.mb.out != nil {
		v.mb.out.parking = true
	}
}

// A sends to B while B is offline. The Outbox parks a sealed copy once. A
// goes away for good, B comes back and files the parked mail as A's, with
// the host as the relay.
func TestParkedMailReachesTheRecipientWhenTheSenderIsGone(t *testing.T) {
	store := newParkStore()
	n := newTestNet()
	a := n.addVisor(t)
	setParker(a, store)
	b := n.addVisor(t)
	setParker(b, store)
	n.setDown(b, true)
	stopA := startOutbox(t, a)

	res, err := a.mb.Send(context.Background(), Outgoing{From: "alice", To: []string{addr("bob", b.pk)}, Subject: "parked", Body: "while you were out"})
	require.NoError(t, err)
	require.True(t, res.Recipients[0].Queued)
	waitFor(t, "A parks it for B", func() bool { return store.count(b.pk) == 1 })
	q := queueOf(t, a)
	require.Len(t, q, 1)
	require.Equal(t, []string{b.pk.Hex()}, q[0].Queue.Parked)

	require.NoError(t, a.mb.Retry(""))
	waitFor(t, "A retries", func() bool { return queueOf(t, a)[0].Queue.Attempts >= 2 })
	require.Equal(t, 1, store.count(b.pk), "parked once")

	stopA()
	n.setDown(a, true)
	n.setDown(b, false)
	require.Equal(t, 1, b.mb.Unpark(context.Background()))
	inbox, err := b.mb.List(FolderInbox)
	require.NoError(t, err)
	require.Len(t, inbox, 1)
	require.Equal(t, "parked", inbox[0].Subject)
	require.Equal(t, a.pk.Hex(), inbox[0].PeerPK, "the signer is the sender")
	require.Equal(t, store.host.Hex(), inbox[0].RelayPK)
	require.Zero(t, store.count(b.pk), "filed mail is removed from the host")
	require.Zero(t, b.mb.Unpark(context.Background()))
}

// Mail that can never be filed is removed. Mail that may file later stays.
func TestUnparkKeepsOnlyWhatMayFileLater(t *testing.T) {
	store := newParkStore()
	n := newTestNet()
	b := n.addVisor(t)
	setParker(b, store)
	stranger, strangerSK := cipher.GenerateKeyPair()

	// Not sealed: dropped.
	_, err := parker{s: store, self: stranger}.Park(context.Background(), b.pk, []byte("Subject: x\r\n\r\nplain\r\n"), "r1")
	require.NoError(t, err)
	require.Zero(t, b.mb.Unpark(context.Background()))
	require.Zero(t, store.count(b.pk))

	// From a key the whitelist refuses: dropped.
	other, _ := cipher.GenerateKeyPair()
	require.NoError(t, b.mb.SetWhitelist([]cipher.PubKey{other}))
	env, err := Seal([]byte("Subject: y\r\n\r\nhi\r\n"), strangerSK, b.pk)
	require.NoError(t, err)
	_, err = parker{s: store, self: stranger}.Park(context.Background(), b.pk, env, "r2")
	require.NoError(t, err)
	require.Zero(t, b.mb.Unpark(context.Background()))
	require.Zero(t, store.count(b.pk))
	require.NoError(t, b.mb.SetWhitelist(nil))

	// Into a full mailbox: kept for later.
	env, err = Seal([]byte("Subject: z\r\n\r\nlater\r\n"), strangerSK, b.pk)
	require.NoError(t, err)
	_, err = b.mb.store(b.mb.inbox, []byte("Subject: filler\r\n\r\nx\r\n"), false)
	require.NoError(t, err)
	b.mb.SetLimits(Limits{MaxTotalSize: 1})
	_, err = parker{s: store, self: stranger}.Park(context.Background(), b.pk, env, "r3")
	require.NoError(t, err)
	require.Zero(t, b.mb.Unpark(context.Background()))
	require.Equal(t, 1, store.count(b.pk), "kept while the mailbox is full")
}

// Mail queued before parking was possible, such as before a restart, is
// parked before the next attempt, which may hang until attemptTimeout.
func TestParkBeforeAHangingAttempt(t *testing.T) {
	store := newParkStore()
	n := newTestNet()
	a := n.addVisor(t)
	b := n.addVisor(t)
	n.setDown(b, true)
	stop := startOutbox(t, a)
	_, err := a.mb.Send(context.Background(), Outgoing{From: "alice", To: []string{addr("bob", b.pk)}, Subject: "s", Body: "b"})
	require.NoError(t, err)
	waitFor(t, "A tries once", func() bool { return queueOf(t, a)[0].Queue.Attempts >= 1 })
	stop()

	setParker(a, store)
	n.mu.Lock()
	n.down[b.pk], n.hang[b.pk] = false, true
	n.mu.Unlock()
	require.NoError(t, a.mb.Retry(""), "due now, as after a reopen")
	stop = startOutbox(t, a)
	waitFor(t, "A parks it while the attempt hangs", func() bool { return store.count(b.pk) == 1 })
	stop()
	// RunOutbox does not wait for an attempt that its end cancels.
	waitFor(t, "a shutdown mid attempt keeps the park", func() bool {
		return slices.Equal(queueOf(t, a)[0].Queue.Parked, []string{b.pk.Hex()})
	})
}

// A park that fails waits for its retry delay instead of looping.
func TestFailedParkBacksOff(t *testing.T) {
	store := newParkStore()
	store.err = errors.New("no inbox host")
	n := newTestNet()
	a := n.addVisor(t)
	setParker(a, store)
	b := n.addVisor(t)
	n.setDown(b, true)
	startOutbox(t, a)
	_, err := a.mb.Send(context.Background(), Outgoing{From: "alice", To: []string{addr("bob", b.pk)}, Subject: "s", Body: "b"})
	require.NoError(t, err)
	waitFor(t, "A tries to park", func() bool {
		store.mu.Lock()
		defer store.mu.Unlock()
		return store.calls >= 1
	})
	time.Sleep(200 * time.Millisecond)
	store.mu.Lock()
	defer store.mu.Unlock()
	require.Equal(t, 1, store.calls)
}

// A park retry that falls due while a delivery attempt could still hang runs
// before that attempt, not after it.
func TestParkRetryBeforeAHangingAttempt(t *testing.T) {
	store := newParkStore()
	store.err = errors.New("not allowed yet")
	n := newTestNet()
	a := n.addVisor(t)
	setParker(a, store)
	b := n.addVisor(t)
	n.setDown(b, true)
	startOutbox(t, a)
	_, err := a.mb.Send(context.Background(), Outgoing{From: "alice", To: []string{addr("bob", b.pk)}, Subject: "s", Body: "b"})
	require.NoError(t, err)
	waitFor(t, "the first park fails", func() bool {
		store.mu.Lock()
		defer store.mu.Unlock()
		return store.calls >= 1
	})

	store.mu.Lock()
	store.err = nil
	store.mu.Unlock()
	n.mu.Lock()
	n.down[b.pk], n.hang[b.pk] = false, true
	n.mu.Unlock()
	require.NoError(t, a.mb.Retry(""), "delivery due now, the park retry still ahead")
	waitFor(t, "A parks it before the attempt hangs", func() bool { return store.count(b.pk) == 1 })
}
