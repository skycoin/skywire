// Package dmsgasync pkg/dmsg/dmsgasync/store_test.go c1-net-dmsg
package dmsgasync

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func pk() cipher.PubKey {
	p, _ := cipher.GenerateKeyPair()
	return p
}

// Both backends must behave the same.
func forEachStore(t *testing.T, lim Limits, fn func(t *testing.T, s *store)) {
	t.Run("memory", func(t *testing.T) {
		s, err := newStore(lim, "", time.Now())
		require.NoError(t, err)
		fn(t, s)
	})
	t.Run("disk", func(t *testing.T) {
		s, err := newStore(lim, t.TempDir(), time.Now())
		require.NoError(t, err)
		fn(t, s)
	})
}

func TestStore_DepositFetchAck(t *testing.T) {
	forEachStore(t, Limits{}, func(t *testing.T, s *store) {
		from, to, other := pk(), pk(), pk()
		now := time.Now()
		id1, err := s.deposit(from, to, 25, "", []byte("one"), Policy{}, now)
		require.NoError(t, err)
		_, err = s.deposit(from, to, 1, "", []byte("two"), Policy{}, now.Add(time.Millisecond))
		require.NoError(t, err)

		msgs, more, err := s.fetch(to, []uint16{25, 1})
		require.NoError(t, err)
		require.False(t, more)
		require.Len(t, msgs, 2)
		require.Equal(t, id1, msgs[0].ID, "oldest first")
		require.Equal(t, []byte("one"), msgs[0].Payload)
		require.Equal(t, from, msgs[0].From)

		only25, _, err := s.fetch(to, []uint16{25})
		require.NoError(t, err)
		require.Len(t, only25, 1, "the port filter applies")

		require.Zero(t, s.ack(other, []string{id1}), "another key cannot ack")
		require.Equal(t, 1, s.ack(to, []string{id1}))
		left, _, err := s.fetch(to, []uint16{25, 1})
		require.NoError(t, err)
		require.Len(t, left, 1)
		require.Equal(t, Stats{Messages: 1, Bytes: 3, Recipients: 1}, s.stats())
	})
}

func TestStore_RefIsIdempotent(t *testing.T) {
	forEachStore(t, Limits{}, func(t *testing.T, s *store) {
		from, to := pk(), pk()
		ref := NewRef()
		a, err := s.deposit(from, to, 25, ref, []byte("x"), Policy{}, time.Now())
		require.NoError(t, err)
		b, err := s.deposit(from, to, 25, ref, []byte("x"), Policy{}, time.Now())
		require.NoError(t, err)
		require.Equal(t, a, b)
		require.Equal(t, 1, s.stats().Messages)
		// The same ref from another sender is a different message.
		_, err = s.deposit(pk(), to, 25, ref, []byte("x"), Policy{}, time.Now())
		require.NoError(t, err)
		require.Equal(t, 2, s.stats().Messages)
	})
}

func TestStore_Limits(t *testing.T) {
	lim := Limits{MaxMessage: 10, MaxPerRecipient: 3, MaxRecipientBytes: 25, MaxTotal: 40}
	forEachStore(t, lim, func(t *testing.T, s *store) {
		to := pk()
		now := time.Now()
		_, err := s.deposit(pk(), to, 25, "", make([]byte, 11), Policy{}, now)
		require.ErrorIs(t, err, ErrTooBig)

		sender := pk()
		pol := Policy{MaxPerSender: 1}
		_, err = s.deposit(sender, to, 25, "", make([]byte, 10), pol, now)
		require.NoError(t, err)
		_, err = s.deposit(sender, to, 25, "", make([]byte, 1), pol, now)
		require.ErrorIs(t, err, ErrSenderFull)

		_, err = s.deposit(pk(), to, 25, "", make([]byte, 10), Policy{}, now)
		require.NoError(t, err)
		_, err = s.deposit(pk(), to, 25, "", make([]byte, 10), Policy{}, now)
		require.ErrorIs(t, err, ErrRecipientFull, "bytes per recipient")

		_, err = s.deposit(pk(), to, 25, "", make([]byte, 5), Policy{}, now)
		require.NoError(t, err)
		_, err = s.deposit(pk(), to, 25, "", make([]byte, 1), Policy{}, now)
		require.ErrorIs(t, err, ErrRecipientFull, "messages per recipient")

		other := pk()
		_, err = s.deposit(pk(), other, 25, "", make([]byte, 10), Policy{}, now)
		require.NoError(t, err)
		_, err = s.deposit(pk(), other, 25, "", make([]byte, 10), Policy{}, now)
		require.ErrorIs(t, err, ErrStoreFull)

		_, err = s.deposit(pk(), to, 0, "", nil, Policy{}, now)
		require.ErrorIs(t, err, ErrBadRequest)
	})
}

func TestStore_SweepDropsExpired(t *testing.T) {
	forEachStore(t, Limits{TTL: time.Hour}, func(t *testing.T, s *store) {
		to := pk()
		now := time.Now()
		_, err := s.deposit(pk(), to, 25, "", []byte("old"), Policy{}, now.Add(-2*time.Hour))
		require.NoError(t, err)
		_, err = s.deposit(pk(), to, 25, "", []byte("new"), Policy{}, now)
		require.NoError(t, err)
		require.Equal(t, 1, s.sweep(now))
		msgs, _, err := s.fetch(to, []uint16{25})
		require.NoError(t, err)
		require.Len(t, msgs, 1)
		require.Equal(t, []byte("new"), msgs[0].Payload)
	})
}

func TestStore_FetchBatches(t *testing.T) {
	forEachStore(t, Limits{MaxPerRecipient: 100}, func(t *testing.T, s *store) {
		to := pk()
		for i := 0; i < fetchBatchCount+3; i++ {
			_, err := s.deposit(pk(), to, 25, "", []byte{byte(i)}, Policy{}, time.Now())
			require.NoError(t, err)
		}
		msgs, more, err := s.fetch(to, []uint16{25})
		require.NoError(t, err)
		require.True(t, more)
		require.Len(t, msgs, fetchBatchCount)
	})
}

// A disk store keeps only headers in memory and reloads after a restart,
// dropping what is broken or expired.
func TestStore_DiskReload(t *testing.T) {
	dir := t.TempDir()
	s, err := newStore(Limits{TTL: time.Hour}, dir, time.Now())
	require.NoError(t, err)
	from, to := pk(), pk()
	ref := NewRef()
	id, err := s.deposit(from, to, 25, ref, []byte("kept"), Policy{}, time.Now())
	require.NoError(t, err)
	_, err = s.deposit(from, to, 25, "", []byte("old"), Policy{}, time.Now().Add(-2*time.Hour))
	require.NoError(t, err)
	for _, m := range s.byRcpt[to] {
		require.Nil(t, m.payload, "the disk store keeps no payload in memory")
	}
	rdir := filepath.Join(dir, to.Hex())
	require.NoError(t, os.WriteFile(filepath.Join(rdir, "00000000000000000001.aa"), []byte("junk"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(rdir, id+".tmp"), []byte("half"), 0o600))

	s2, err := newStore(Limits{TTL: time.Hour}, dir, time.Now())
	require.NoError(t, err)
	require.Equal(t, Stats{Messages: 1, Bytes: 4, Recipients: 1}, s2.stats())
	msgs, _, err := s2.fetch(to, []uint16{25})
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, id, msgs[0].ID)
	require.Equal(t, []byte("kept"), msgs[0].Payload)
	again, err := s2.deposit(from, to, 25, ref, []byte("kept"), Policy{}, time.Now())
	require.NoError(t, err)
	require.Equal(t, id, again, "the ref survives a restart")
	entries, err := os.ReadDir(rdir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "broken, half-written and expired files are removed")
}

func TestRecord_SignVerify(t *testing.T) {
	rpk, rsk := cipher.GenerateKeyPair()
	host := pk()
	rec, err := NewRecord(rpk, rsk, []cipher.PubKey{host}, 4096, 1, 24*time.Hour)
	require.NoError(t, err)
	require.NoError(t, rec.Verify(time.Now()))
	require.True(t, rec.Names(host))

	tampered := rec
	tampered.Hosts = []cipher.PubKey{pk()}
	require.ErrorIs(t, tampered.Verify(time.Now()), ErrBadRecord)

	require.ErrorIs(t, rec.Verify(rec.Expires.Add(time.Second)), ErrBadRecord, "expired")

	_, otherSK := cipher.GenerateKeyPair()
	forged := rec
	require.NoError(t, forged.Sign(otherSK))
	require.ErrorIs(t, forged.Verify(time.Now()), ErrBadRecord, "signed by another key")

	tooMany, err := NewRecord(rpk, rsk, []cipher.PubKey{pk(), pk(), pk(), pk(), pk()}, 0, 1, time.Hour)
	require.NoError(t, err)
	require.ErrorIs(t, tooMany.Verify(time.Now()), ErrBadRecord)
}

func TestPolicy_Allows(t *testing.T) {
	a, b := pk(), pk()
	require.True(t, Policy{}.Allows(a), "an empty allow list takes every key")
	require.True(t, Policy{Allow: []cipher.PubKey{a}}.Allows(a))
	require.False(t, Policy{Allow: []cipher.PubKey{a}}.Allows(b))
	require.False(t, Policy{Deny: []cipher.PubKey{a}}.Allows(a))
	require.False(t, Policy{Allow: []cipher.PubKey{a}, Deny: []cipher.PubKey{a}}.Allows(a), "deny wins")
}
