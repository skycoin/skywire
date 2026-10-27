// Package visor pkg/visor/pressure_test.go
package visor

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/ar/arfeed"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func TestNextBusy(t *testing.T) {
	const mib = 1 << 20
	piZero := int64(448 * mib)
	host := int64(8192 * mib)
	for _, tc := range []struct {
		name         string
		busy         bool
		avail, total int64
		gc           float64
		want         bool
	}{
		{"pi zero with room", false, 120 * mib, piZero, 0.05, false},
		{"pi zero short of memory", false, 40 * mib, piZero, 0.05, true},
		{"pi zero stays busy until 20%", true, 80 * mib, piZero, 0.05, true},
		{"pi zero recovers", true, 100 * mib, piZero, 0.05, false},
		{"host with 1.3 GB free", false, 1300 * mib, host, 0.02, false},
		{"host with 200 MB free", false, 200 * mib, host, 0.02, true},
		{"host recovers past 512 MB", true, 600 * mib, host, 0.02, false},
		{"gc heavy", false, 0, 0, 0.30, true},
		{"gc settling", true, 0, 0, 0.15, true},
		{"gc recovered", true, 0, 0, 0.05, false},
		{"memory unknown", false, 0, 0, 0.05, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, nextBusy(tc.busy, tc.avail, tc.total, tc.gc))
		})
	}
}

func TestGCSamplerShare(t *testing.T) {
	s := newGCSampler()
	now := time.Now()
	share := s.share(now)
	require.GreaterOrEqual(t, share, 0.0)
	require.LessOrEqual(t, share, 1.0)
	for i := 1; i <= 10; i++ {
		s.share(now.Add(time.Duration(i) * pressureInterval))
	}
	// Only readings inside the window, plus the one that opens it, are kept.
	require.LessOrEqual(t, len(s.hist), int(pressureWindow/pressureInterval)+2)
}

func TestInboundGate(t *testing.T) {
	hvPK, _ := cipher.GenerateKeyPair()
	childPK, _ := cipher.GenerateKeyPair()
	otherPK, _ := cipher.GenerateKeyPair()
	v := &Visor{
		conf:       &visorconfig.V1{Hypervisors: []cipher.PubKey{hvPK}},
		hvInstance: &Hypervisor{mu: new(sync.RWMutex), remoteVisors: map[cipher.PubKey]Conn{childPK: {}}},
	}

	require.True(t, v.inboundGate(otherPK, transport.LabelAutomatic), "not busy takes everything")

	v.pressure.busy.Store(true)
	require.False(t, v.inboundGate(otherPK, transport.LabelAutomatic))
	require.True(t, v.inboundGate(otherPK, transport.LabelUser))
	require.True(t, v.inboundGate(otherPK, transport.LabelSkycoin))
	require.True(t, v.inboundGate(hvPK, transport.LabelAutomatic), "own hypervisor")
	require.True(t, v.inboundGate(childPK, transport.LabelAutomatic), "visor this one is the hypervisor of")
}

func TestReachDeclUnderPressure(t *testing.T) {
	v := &Visor{initLock: new(sync.RWMutex), tpM: &transport.Manager{}}
	v.pressure.busy.Store(true)
	d := v.reachDecl()
	require.Equal(t, []string{arfeed.AcceptsNone}, d.Accepts)
	require.Empty(t, d.Inbound)

	r := &arfeed.Reach{ReachDecl: d, Bound: []string{arfeed.TypeSUDPH, arfeed.TypeSTCPR}, Open: []string{arfeed.TypeSTCPR}, Live: true}
	for _, typ := range []string{arfeed.TypeSTCPR, arfeed.TypeSUDPH, arfeed.TypeQUIC, arfeed.TypeWT, arfeed.TypeWEBRTC} {
		require.Equal(t, arfeed.Unreachable, r.Verdict(typ, arfeed.NATFullCone, time.Now()), typ)
	}
}
