// Package dmsgasync pkg/dmsg/dmsgasync/host_test.go c1-net-dmsg
package dmsgasync

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// pipeDialer connects as `self` to the hosts in the map, handing each the
// caller's key the way a dmsg stream's Noise handshake would.
func pipeDialer(self cipher.PubKey, hosts map[cipher.PubKey]*Host) Dialer {
	return func(_ context.Context, pk cipher.PubKey, port uint16) (net.Conn, error) {
		h, ok := hosts[pk]
		if !ok || port != Port {
			return nil, errors.New("unreachable")
		}
		c, s := net.Pipe()
		go h.Handle(s, self)
		return c, nil
	}
}

type party struct {
	pk cipher.PubKey
	sk cipher.SecKey
}

func newParty() party {
	p, s := cipher.GenerateKeyPair()
	return party{p, s}
}

func newTestHost(t *testing.T, store string, accepts ...cipher.PubKey) (*Host, cipher.PubKey) {
	t.Helper()
	hpk := pk()
	h, err := NewHost(HostConfig{
		PK:    hpk,
		Store: store,
		Dir:   t.TempDir(),
		Accepts: func(k cipher.PubKey) bool {
			for _, a := range accepts {
				if a == k {
					return true
				}
			}
			return false
		},
	})
	require.NoError(t, err)
	return h, hpk
}

func TestHost_EndToEnd(t *testing.T) {
	for _, store := range []string{StoreDisk, StoreMemory} {
		t.Run(store, func(t *testing.T) {
			ctx := context.Background()
			rcpt, friend, stranger := newParty(), newParty(), newParty()
			h, hpk := newTestHost(t, store, rcpt.pk)
			hosts := map[cipher.PubKey]*Host{hpk: h}

			rec, err := NewRecord(rcpt.pk, rcpt.sk, []cipher.PubKey{hpk}, 0, 1, time.Hour)
			require.NoError(t, err)
			pol := Policy{Allow: []cipher.PubKey{friend.pk}}
			require.NoError(t, Register(ctx, pipeDialer(rcpt.pk, hosts), hpk, rec, pol))

			got, err := LookupRecord(ctx, pipeDialer(stranger.pk, hosts), hpk, rcpt.pk)
			require.NoError(t, err)
			require.Equal(t, rec.Seq, got.Seq)

			_, err = Deposit(ctx, pipeDialer(stranger.pk, hosts), hpk, rcpt.pk, 25, "", []byte("spam"))
			require.ErrorIs(t, err, ErrNotAllowed, "the recipient's policy applies")

			ref := NewRef()
			host, err := DepositAny(ctx, pipeDialer(friend.pk, hosts), got, 25, ref, []byte("hello"))
			require.NoError(t, err)
			require.Equal(t, hpk, host)
			_, err = DepositAny(ctx, pipeDialer(friend.pk, hosts), got, 25, ref, []byte("hello"))
			require.NoError(t, err)
			require.Equal(t, 1, h.Stats().Messages, "a retried deposit is stored once")

			var seen []Message
			n, err := Fetch(ctx, pipeDialer(rcpt.pk, hosts), hpk, []uint16{25}, func(m Message) error {
				seen = append(seen, m)
				return nil
			})
			require.NoError(t, err)
			require.Equal(t, 1, n)
			require.Equal(t, friend.pk, seen[0].From, "the sender is the authenticated key")
			require.Equal(t, []byte("hello"), seen[0].Payload)
			require.Equal(t, hpk, seen[0].Host)
			require.Zero(t, h.Stats().Messages, "acked messages are deleted")
		})
	}
}

func TestHost_HandlerErrorKeepsMessage(t *testing.T) {
	ctx := context.Background()
	rcpt, friend := newParty(), newParty()
	h, hpk := newTestHost(t, StoreMemory, rcpt.pk)
	hosts := map[cipher.PubKey]*Host{hpk: h}
	rec, err := NewRecord(rcpt.pk, rcpt.sk, []cipher.PubKey{hpk}, 0, 1, time.Hour)
	require.NoError(t, err)
	require.NoError(t, Register(ctx, pipeDialer(rcpt.pk, hosts), hpk, rec, Policy{}))
	_, err = Deposit(ctx, pipeDialer(friend.pk, hosts), hpk, rcpt.pk, 25, "", []byte("x"))
	require.NoError(t, err)

	n, err := Fetch(ctx, pipeDialer(rcpt.pk, hosts), hpk, []uint16{25}, func(Message) error { return errors.New("busy") })
	require.NoError(t, err)
	require.Zero(t, n)
	require.Equal(t, 1, h.Stats().Messages)

	// Another key fetching gets nothing of the recipient's.
	n, err = Fetch(ctx, pipeDialer(friend.pk, hosts), hpk, []uint16{25}, func(Message) error { return nil })
	require.NoError(t, err)
	require.Zero(t, n)
	require.Equal(t, 1, h.Stats().Messages)
}

func TestHost_RegistrationRules(t *testing.T) {
	ctx := context.Background()
	rcpt, other := newParty(), newParty()
	h, hpk := newTestHost(t, StoreMemory, rcpt.pk)
	hosts := map[cipher.PubKey]*Host{hpk: h}
	dial := pipeDialer(rcpt.pk, hosts)

	notNamed, err := NewRecord(rcpt.pk, rcpt.sk, []cipher.PubKey{pk()}, 0, 1, time.Hour)
	require.NoError(t, err)
	require.ErrorIs(t, Register(ctx, dial, hpk, notNamed, Policy{}), ErrBadRecord, "the record must name the host")

	theirs, err := NewRecord(other.pk, other.sk, []cipher.PubKey{hpk}, 0, 1, time.Hour)
	require.NoError(t, err)
	require.ErrorIs(t, Register(ctx, dial, hpk, theirs, Policy{}), ErrBadRecord, "a key registers only itself")
	require.ErrorIs(t, Register(ctx, pipeDialer(other.pk, hosts), hpk, theirs, Policy{}), ErrNotHosted, "the host must accept the key")

	big, err := NewRecord(rcpt.pk, rcpt.sk, []cipher.PubKey{hpk}, MaxMessageLimit, 1, time.Hour)
	require.NoError(t, err)
	require.ErrorIs(t, Register(ctx, dial, hpk, big, Policy{}), ErrTooBig)

	v2, err := NewRecord(rcpt.pk, rcpt.sk, []cipher.PubKey{hpk}, 0, 2, time.Hour)
	require.NoError(t, err)
	require.NoError(t, Register(ctx, dial, hpk, v2, Policy{}))
	v1, err := NewRecord(rcpt.pk, rcpt.sk, []cipher.PubKey{hpk}, 0, 1, time.Hour)
	require.NoError(t, err)
	require.ErrorIs(t, Register(ctx, dial, hpk, v1, Policy{}), ErrBadRecord, "an older record does not replace a newer one")

	_, err = Deposit(ctx, pipeDialer(pk(), hosts), hpk, other.pk, 25, "", []byte("x"))
	require.ErrorIs(t, err, ErrNotHosted, "no deposit for an unregistered key")

	_, err = Deposit(ctx, pipeDialer(other.pk, hosts), hpk, rcpt.pk, 25, "", []byte("x"))
	require.NoError(t, err)
	dropped, err := Unregister(ctx, dial, hpk)
	require.NoError(t, err)
	require.Equal(t, 1, dropped)
	_, err = Deposit(ctx, pipeDialer(other.pk, hosts), hpk, rcpt.pk, 25, "", []byte("x"))
	require.ErrorIs(t, err, ErrNotHosted)
}

func TestHost_RecordSizeBoundsDeposit(t *testing.T) {
	ctx := context.Background()
	rcpt := newParty()
	h, hpk := newTestHost(t, StoreMemory, rcpt.pk)
	hosts := map[cipher.PubKey]*Host{hpk: h}
	rec, err := NewRecord(rcpt.pk, rcpt.sk, []cipher.PubKey{hpk}, 8, 1, time.Hour)
	require.NoError(t, err)
	require.NoError(t, Register(ctx, pipeDialer(rcpt.pk, hosts), hpk, rec, Policy{}))
	_, err = Deposit(ctx, pipeDialer(pk(), hosts), hpk, rcpt.pk, 25, "", make([]byte, 9))
	require.ErrorIs(t, err, ErrTooBig)
}

// A disk host keeps its registrations across a restart.
func TestHost_DiskKeepsRegistrations(t *testing.T) {
	ctx := context.Background()
	rcpt := newParty()
	dir := t.TempDir()
	hpk := pk()
	cfg := HostConfig{PK: hpk, Dir: dir, Accepts: func(cipher.PubKey) bool { return true }}
	h, err := NewHost(cfg)
	require.NoError(t, err)
	rec, err := NewRecord(rcpt.pk, rcpt.sk, []cipher.PubKey{hpk}, 0, 1, time.Hour)
	require.NoError(t, err)
	require.NoError(t, Register(ctx, pipeDialer(rcpt.pk, map[cipher.PubKey]*Host{hpk: h}), hpk, rec, Policy{}))
	_, err = Deposit(ctx, pipeDialer(pk(), map[cipher.PubKey]*Host{hpk: h}), hpk, rcpt.pk, 25, "", []byte("x"))
	require.NoError(t, err)

	h2, err := NewHost(cfg)
	require.NoError(t, err)
	require.Equal(t, []cipher.PubKey{rcpt.pk}, h2.Registered())
	require.Equal(t, 1, h2.Stats().Messages)
}

func TestHost_DepositAnyFallsThrough(t *testing.T) {
	ctx := context.Background()
	rcpt := newParty()
	h, hpk := newTestHost(t, StoreMemory, rcpt.pk)
	down := pk()
	hosts := map[cipher.PubKey]*Host{hpk: h}
	rec, err := NewRecord(rcpt.pk, rcpt.sk, []cipher.PubKey{down, hpk}, 0, 1, time.Hour)
	require.NoError(t, err)
	require.NoError(t, Register(ctx, pipeDialer(rcpt.pk, hosts), hpk, rec, Policy{}))
	got, err := DepositAny(ctx, pipeDialer(pk(), hosts), rec, 25, "", []byte("x"))
	require.NoError(t, err)
	require.Equal(t, hpk, got, "an unreachable first host is skipped")
}
