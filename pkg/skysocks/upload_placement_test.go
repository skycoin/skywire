// Package skysocks pkg/skysocks/upload_placement_test.go — where a striped
// upload's chunks go.
//
// The rows these tests are written against, all from
// bench/2026-09-16/e128db1ff-smoke/mux-tunnels-2 (10 MB uploads, four 2.5 MB
// chunks over a 31.6 ms and a 135.8 ms tunnel): 4/0 ran 1.61 s, x1.107 of the
// single-route bar; 3/1 ran x0.393, its one slow chunk taking ~3.6 s at
// ~0.7 MB/s while the fast tunnel idled 2.6 s of it; 2/1/1, with one chunk on a
// third tunnel created mid-set and never measured, ran x0.265.
package skysocks

import (
	"net/http"
	"testing"
	"time"

	"github.com/0magnet/yamux"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
)

// countLanes tallies spreadBurst's answer per tunnel.
func countLanes(lanes []int, k int) []int {
	out := make([]int, k)
	for _, l := range lanes {
		if l >= 0 && l < k {
			out[l]++
		}
	}
	return out
}

// (a) Four chunks over a 6 MB/s and a 0.7 MB/s tunnel. The slow tunnel's whole
// quota is floor(4 × 0.7/6.7) = 0, so it takes at most one chunk and never the
// last one — the 3/1 row is the assignment this forbids.
func TestUploadBurstKeepsTheSlowTunnelOffTheTail(t *testing.T) {
	lanes := spreadBurst([]float64{6e6, 0.7e6}, 4)
	require.Len(t, lanes, 4)
	counts := countLanes(lanes, 2)
	require.LessOrEqual(t, counts[1], 1, "the 0.7 MB/s tunnel takes at most one of four chunks")
	require.Equal(t, 0, lanes[len(lanes)-1], "the object's last chunk is the fast tunnel's")
	require.Equal(t, 4, counts[0]+counts[1], "every chunk is placed")
}

// (b) The same pair plus a third tunnel nothing has ever measured. It is
// credited the MEDIAN of the two measured tunnels, not the best of them, so it
// takes one chunk rather than the share a 6 MB/s tunnel would — and still not
// the last one. This is the 2/1/1 row.
func TestUploadBurstDoesNotAssumeAnUnmeasuredTunnelIsFastest(t *testing.T) {
	lanes := spreadBurst([]float64{6e6, 0.7e6, 0}, 4)
	require.Len(t, lanes, 4)
	counts := countLanes(lanes, 3)
	require.LessOrEqual(t, counts[2], 1, "an unmeasured tunnel takes at most one of four chunks")
	require.LessOrEqual(t, counts[1], 1, "the 0.7 MB/s tunnel takes at most one")
	require.Equal(t, 0, lanes[len(lanes)-1], "the object's last chunk is the measured fast tunnel's")
}

// With nothing measured anywhere there is no slow tunnel to keep a chunk off
// and no median to credit: the chunks go round-robin, which is equal shares.
func TestUploadBurstWithNothingMeasuredIsEqualShares(t *testing.T) {
	counts := countLanes(spreadBurst([]float64{0, 0, 0}, 6), 3)
	require.Equal(t, []int{2, 2, 2}, counts)
}

// medianMeasured ignores the tunnels that have proven nothing, and averages the
// middle pair when the measured ones are even in number.
func TestMedianMeasuredIgnoresTheUnproven(t *testing.T) {
	require.Zero(t, medianMeasured([]float64{0, 0}))
	require.InDelta(t, 6e6, medianMeasured([]float64{6e6, 0}), 1)
	require.InDelta(t, 3.35e6, medianMeasured([]float64{6e6, 0.7e6, 0}), 1)
	require.InDelta(t, 5e6, medianMeasured([]float64{8e6, 5e6, 2e6}), 1)
}

// planBurst turns that assignment into sessions: a 10 MB object cut into four
// chunks over a fast and a slow tunnel places nothing on the slow one, and the
// chunk at the object's last offset is the fast tunnel's.
func TestPlanBurstPlacesTheTailChunkOnTheFastTunnel(t *testing.T) {
	fast, close0 := newTestSession(t)
	defer close0()
	slow, close1 := newTestSession(t)
	defer close1()
	now := time.Now()
	mFast, mSlow := new(tunnelMeter), new(tunnelMeter)
	mFast.txCapBps, mFast.rxCapBps, mFast.busyAt = 6e6, 6e6, now
	mSlow.txCapBps, mSlow.rxCapBps, mSlow.busyAt = 0.7e6, 0.7e6, now
	c := &Client{
		sessions:  []*yamux.Session{fast, slow},
		recvStamp: map[*yamux.Session]*tunnelMeter{fast: mFast, slow: mSlow},
		standby:   map[*yamux.Session]bool{},
		closeC:    make(chan struct{}),
	}
	const chunk = int64(2 << 20)
	s := newUploadStripe(c, &uploadCandidate{
		req: &http.Request{}, host: "sink.example", total: 4 * chunk, stripe: true,
	}, nil)
	s.planned.Store(chunk)
	tun := s.tunables()
	require.EqualValues(t, chunk, tun.chunk)

	b := s.planBurst(tun, s.slotsFrom(tun))
	require.NotNil(t, b, "four chunks fit the admission gate in one burst")
	require.Same(t, fast, b.take(3*chunk), "the last chunk goes to the fast tunnel")
	for i := int64(0); i < 3; i++ {
		require.Same(t, fast, b.take(i*chunk), "chunk %d", i)
	}
	require.Nil(t, b.take(0), "a chunk is placed once; a re-send goes through the picker")
}

// An object with more chunks than the admission gate has a tail placed with
// feedback — the 50 MB cell, which measured fine — so it is left to the picker.
func TestPlanBurstDeclinesAnObjectWithFeedback(t *testing.T) {
	fast, close0 := newTestSession(t)
	defer close0()
	slow, close1 := newTestSession(t)
	defer close1()
	c := &Client{
		sessions:  []*yamux.Session{fast, slow},
		recvStamp: map[*yamux.Session]*tunnelMeter{fast: new(tunnelMeter), slow: new(tunnelMeter)},
		standby:   map[*yamux.Session]bool{},
		closeC:    make(chan struct{}),
	}
	const chunk = int64(2 << 20)
	s := newUploadStripe(c, &uploadCandidate{
		req: &http.Request{}, host: "sink.example", total: 40 * chunk, stripe: true,
	}, nil)
	s.planned.Store(chunk)
	require.Nil(t, s.planBurst(s.tunables(), s.slotsFrom(s.tunables())))
}

// The picker's upload direction: a striped-upload chunk is scored on the
// tunnels' UPLOAD capacity. The rig's direct tunnel is the slow one DOWN and
// the fast one UP, so the two directions disagree about it — and a download's
// answer is unchanged.
func TestPickSendWeighsTheUploadCapacity(t *testing.T) {
	s0, close0 := newTestSession(t)
	defer close0()
	s1, close1 := newTestSession(t)
	defer close1()
	now := time.Now()
	direct, twoHop := new(tunnelMeter), new(tunnelMeter)
	direct.rxCapBps, direct.txCapBps, direct.busyAt = 2e6, 9e6, now
	twoHop.rxCapBps, twoHop.txCapBps, twoHop.busyAt = 3e6, 5e6, now
	c := &Client{
		sessions:  []*yamux.Session{s1, s0},
		recvStamp: map[*yamux.Session]*tunnelMeter{s0: direct, s1: twoHop},
		closeC:    make(chan struct{}),
	}
	require.Same(t, s1, c.pickSessionFor(pickRecv), "a range chunk still weighs download capacity")
	require.Same(t, s0, c.pickSessionFor(pickSend), "an upload chunk weighs upload capacity")
}

// The credit an unmeasured tunnel gets, in the picker rather than the plan: a
// download probes it (best capacity), an upload does not (median). Same three
// tunnels, same call, two answers.
func TestPickSendCreditsAnUnmeasuredTunnelTheMedian(t *testing.T) {
	cold, close0 := newTestSession(t)
	defer close0()
	fast, close1 := newTestSession(t)
	defer close1()
	slow, close2 := newTestSession(t)
	defer close2()
	now := time.Now()
	mFast, mSlow := new(tunnelMeter), new(tunnelMeter)
	mFast.rxCapBps, mFast.txCapBps, mFast.busyAt = 8e6, 8e6, now
	mSlow.rxCapBps, mSlow.txCapBps, mSlow.busyAt = 2e6, 2e6, now
	c := &Client{
		sessions: []*yamux.Session{cold, fast, slow},
		recvStamp: map[*yamux.Session]*tunnelMeter{
			cold: new(tunnelMeter), fast: mFast, slow: mSlow,
		},
		closeC: make(chan struct{}),
	}
	require.Same(t, cold, c.pickSessionFor(pickRecv), "a download credits the unproven tunnel the best capacity")
	require.Same(t, fast, c.pickSessionFor(pickSend), "an upload credits it the median, so the proven fast tunnel wins")
}

// The RTT term: at equal upload capacity per stream the lower-RTT tunnel takes
// the chunk. A download's tie is still broken on the stream count alone, so the
// session order decides it exactly as it did.
func TestPickSendBreaksACapacityTieOnRTT(t *testing.T) {
	far, close0 := newTestSession(t)
	defer close0()
	near, close1 := newTestSession(t)
	defer close1()
	now := time.Now()
	mFar, mNear := new(tunnelMeter), new(tunnelMeter)
	mFar.rxCapBps, mFar.txCapBps, mFar.busyAt, mFar.rttMs = 5e6, 5e6, now, 135.8
	mNear.rxCapBps, mNear.txCapBps, mNear.busyAt, mNear.rttMs = 5e6, 5e6, now, 31.6
	c := &Client{
		sessions:  []*yamux.Session{far, near},
		recvStamp: map[*yamux.Session]*tunnelMeter{far: mFar, near: mNear},
		closeC:    make(chan struct{}),
	}
	require.Same(t, near, c.pickSessionFor(pickSend), "at equal capacity the 31.6 ms tunnel wins")
	require.Same(t, far, c.pickSessionFor(pickRecv), "a download's tie is still the session order")
}

// upload.burst_plan off is the old code path, not a re-implementation of it:
// every chunk goes through the picker.
func TestUploadBurstPlanKnobTurnsThePlanOff(t *testing.T) {
	fast, close0 := newTestSession(t)
	defer close0()
	slow, close1 := newTestSession(t)
	defer close1()
	c := &Client{
		sessions:  []*yamux.Session{fast, slow},
		recvStamp: map[*yamux.Session]*tunnelMeter{fast: new(tunnelMeter), slow: new(tunnelMeter)},
		standby:   map[*yamux.Session]bool{},
		closeC:    make(chan struct{}),
	}
	const chunk = int64(2 << 20)
	s := newUploadStripe(c, &uploadCandidate{
		req: &http.Request{}, host: "sink.example", total: 4 * chunk, stripe: true,
	}, nil)
	s.planned.Store(chunk)
	require.NotNil(t, s.planBurst(s.tunables(), s.slotsFrom(s.tunables())))

	require.True(t, skysettings.Apply(map[string]int64{skysettings.UploadBurstPlan: 0}))
	t.Cleanup(func() { skysettings.Reset() })
	require.Nil(t, s.planBurst(s.tunables(), s.slotsFrom(s.tunables())))
}
