// Package api pkg/deployment/ar/api/dial_assist_log_test.go c4-net-discovery
package api

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// testPKs returns n distinct, valid public keys. cipher.PubKey validates the
// point, so hand-built byte patterns are rejected.
func testPKs(t *testing.T, n int) []cipher.PubKey {
	t.Helper()
	pks := make([]cipher.PubKey, n)
	for i := range pks {
		pk, _ := cipher.GenerateKeyPair()
		pks[i] = pk
	}
	return pks
}

// TestDialAssistFailuresCollapsesWindow is the point of the change: many
// skipped dial-assists inside one window produce ONE summary, not one line
// each, and the summary carries the rate and the distinct-peer count.
func TestDialAssistFailuresCollapsesWindow(t *testing.T) {
	d := newDialAssistFailures(time.Minute)
	start := time.Now()

	pks := testPKs(t, 2)
	pkA, pkB := pks[0], pks[1]

	// 1000 failures spread over 59s across two peers: nothing emitted yet.
	summaries := 0
	for i := 0; i < 1000; i++ {
		pk := pkA
		if i%4 == 0 {
			pk = pkB
		}
		if s := d.record(start.Add(time.Duration(i)*59*time.Millisecond), pk); s != nil {
			summaries++
		}
	}
	require.Zero(t, summaries, "no summary before the window closes")

	// One more past the minute closes it.
	s := d.record(start.Add(61*time.Second), pkA)
	require.NotNil(t, s)
	require.Equal(t, 1001, s.Count)
	require.Equal(t, 2, s.Peers)
	require.Equal(t, 61*time.Second, s.Window)
	// pkA took 750 of the first 1000 plus the closing one.
	require.Equal(t, []string{pkA.Hex() + "=751", pkB.Hex() + "=250"}, s.TopPeers)

	// Counters reset for the next window.
	require.Nil(t, d.record(start.Add(62*time.Second), pkA))
	s2 := d.record(start.Add(2*time.Minute+2*time.Second), pkB)
	require.NotNil(t, s2)
	require.Equal(t, 2, s2.Count)
	require.Equal(t, 2, s2.Peers)
}

// TestDialAssistFailuresTopPeersBounded keeps the summary line a fixed size no
// matter how many distinct peers are offline.
func TestDialAssistFailuresTopPeersBounded(t *testing.T) {
	d := newDialAssistFailures(time.Minute)
	start := time.Now()

	pks := testPKs(t, 50)
	for i := 0; i < 50; i++ {
		pk := pks[i]
		// Peer i gets i+1 records, so the ranking is deterministic.
		for j := 0; j <= i; j++ {
			require.Nil(t, d.record(start, pk))
		}
	}

	s := d.record(start.Add(time.Minute), pks[0])
	require.NotNil(t, s)
	require.Equal(t, 50, s.Peers)
	require.Len(t, s.TopPeers, dialAssistTopPeers)
	// Highest count first: peer 50 recorded 50 times.
	require.Equal(t, pks[49].Hex()+"=50", s.TopPeers[0])
}

// TestDialAssistFailuresConcurrent exercises the lock: /resolve runs one
// goroutine per request.
func TestDialAssistFailuresConcurrent(t *testing.T) {
	d := newDialAssistFailures(time.Millisecond)
	pks := testPKs(t, 16)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			pk := pks[n]
			for j := 0; j < 200; j++ {
				d.record(time.Now(), pk)
			}
		}(i)
	}
	wg.Wait()
}
