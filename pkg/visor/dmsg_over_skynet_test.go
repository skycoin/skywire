package visor

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestDmsgSkynetSkipsRelaysThatCannotReachDst(t *testing.T) {
	var s dmsgSkynetStats
	dst, _ := cipher.GenerateKeyPair()
	r1, _ := cipher.GenerateKeyPair()
	r2, _ := cipher.GenerateKeyPair()
	e2e := "vstream relay: noise handshake with " + dst.String() + ": EOF"

	// One relay failed for another reason: relays may still work.
	s.note(dst, &relayDialError{dst: dst, candidates: 2, tries: []relayTry{
		{relay: r1, err: e2e}, {relay: r2, err: "vstream: no non-DMSG transport to relay"},
	}})
	require.False(t, s.skipRelay(dst))

	// Every relay ended in the end-to-end handshake: skip the tier.
	s.note(dst, &relayDialError{dst: dst, candidates: 2, tries: []relayTry{
		{relay: r1, err: e2e}, {relay: r2, err: e2e},
	}})
	require.True(t, s.skipRelay(dst))

	s.mu.Lock()
	s.relaySkip[dst] = time.Now().Add(-time.Second)
	s.mu.Unlock()
	require.False(t, s.skipRelay(dst))

	d := &routerSkynetDialer{noRelay: true}
	_, err := d.dialDirectOrRelay(context.Background(), dst, 80)
	require.ErrorIs(t, err, errNoRelay)
	require.Contains(t, err.Error(), "relays skipped")
}

func TestDmsgSkynetStatsRecordsRelayFailures(t *testing.T) {
	var s dmsgSkynetStats
	svc, _ := cipher.GenerateKeyPair()
	host, _ := cipher.GenerateKeyPair()
	r1, _ := cipher.GenerateKeyPair()
	r2, _ := cipher.GenerateKeyPair()

	rerr := &relayDialError{
		dst: host, direct: "no transport", candidates: 2, confirmed: 1,
		tries: []relayTry{
			{relay: r1, confirmed: true, stage: "handshake", err: "timed out"},
			{relay: r2, stage: "relay", err: "closed"},
		},
	}
	err := fmt.Errorf("service via host: %w", rerr)
	require.ErrorIs(t, err, errNoRelay)
	s.note(svc, err)
	s.note(svc, nil)

	snap := s.snapshot()
	require.EqualValues(t, 1, snap.Skynet)
	require.EqualValues(t, 1, snap.Fallback)
	require.Len(t, snap.Failures, 1)
	f := snap.Failures[0]
	require.Equal(t, svc.String(), f.PK)
	require.Equal(t, host.String(), f.Dst)
	require.Equal(t, "no transport", f.Direct)
	require.Equal(t, 2, f.Candidates)
	require.Equal(t, 1, f.Confirmed)
	require.Len(t, f.Relays, 2)

	for i := 0; i < dmsgSkynetFailureRing+5; i++ {
		s.note(host, errors.New("other"))
	}
	snap = s.snapshot()
	require.Len(t, snap.Failures, dmsgSkynetFailureRing)
	require.Empty(t, snap.Failures[0].Dst)
	require.Equal(t, "other", snap.Failures[len(snap.Failures)-1].Error)
}
