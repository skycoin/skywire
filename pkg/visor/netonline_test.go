package visor

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

func TestOnlineStateMachine(t *testing.T) {
	var s onlineState
	t0 := time.Unix(1000, 0)
	at := func(d time.Duration) time.Time { return t0.Add(d) }

	line, back := s.observe(at(0), 0, 0, "")
	require.Empty(t, line)
	require.False(t, back)
	require.Equal(t, visorapi.ConnConnecting, s.snapshot().State)

	// Neither a transport nor a session within the grace reads as offline.
	line, _ = s.observe(at(offlineGrace), 0, 0, "")
	require.Equal(t, "network: offline (no transports or dmsg sessions for 30s)", line)
	require.False(t, s.snapshot().Online)

	// A dmsg session alone leaves offline for dmsg-only, a return.
	line, back = s.observe(at(31*time.Second), 0, 2, "")
	require.Equal(t, "network: dmsg-only (no transports, 2 dmsg sessions)", line)
	require.True(t, back)
	c := s.snapshot()
	require.False(t, c.Online)
	require.Equal(t, 2, c.DmsgSessions)

	// dmsg-only to online is no return.
	line, back = s.observe(at(32*time.Second), 3, 2, "")
	require.Equal(t, "network: online (3 transports, 2 dmsg sessions)", line)
	require.False(t, back)
	c = s.snapshot()
	require.True(t, c.Online)
	require.Equal(t, 3, c.Transports)

	// Transports alone keep it online, and the reason follows the counts.
	line, _ = s.observe(at(33*time.Second), 1, 0, "")
	require.Empty(t, line)
	require.Equal(t, "1 transports, 0 dmsg sessions", s.snapshot().Reason)

	// Online back to dmsg-only is no return either.
	line, back = s.observe(at(34*time.Second), 0, 1, "")
	require.Contains(t, line, "dmsg-only")
	require.False(t, back)

	// Losing everything waits out the grace before going offline.
	lost := 34 * time.Second
	line, _ = s.observe(at(lost+offlineGrace-time.Second), 0, 0, "")
	require.Empty(t, line)
	require.Equal(t, visorapi.ConnDmsgOnly, s.snapshot().State)
	line, _ = s.observe(at(lost+offlineGrace), 0, 0, "")
	require.Contains(t, line, "offline")

	// Offline straight to online is a return.
	_, back = s.observe(at(lost+offlineGrace+time.Second), 1, 0, "")
	require.True(t, back)
	require.EqualValues(t, 6, s.snapshot().Changes)
}

func TestOnlineConnectingIsNotAReturn(t *testing.T) {
	for _, c := range []struct{ tps, ses int }{{1, 0}, {0, 1}} {
		var s onlineState
		line, back := s.observe(time.Unix(1, 0), c.tps, c.ses, "")
		require.NotEmpty(t, line)
		require.False(t, back)
	}
}

func TestOnlineReportedOfflineOutweighsTransports(t *testing.T) {
	var s onlineState
	t0 := time.Unix(1000, 0)
	s.observe(t0, 2, 1, "")
	s.report(t0, true, "browser")
	line, _ := s.observe(t0.Add(time.Second), 2, 1, "")
	require.Equal(t, "network: offline (browser reported no network)", line)
	// The report expires, and a later report of a network clears it.
	line, back := s.observe(t0.Add(reportedOfflineHold), 2, 1, "")
	require.Contains(t, line, "online")
	require.True(t, back)
	s.report(t0.Add(reportedOfflineHold), true, "android")
	s.observe(t0.Add(reportedOfflineHold+time.Second), 2, 1, "")
	require.False(t, s.snapshot().Online)
	s.report(t0.Add(reportedOfflineHold+2*time.Second), false, "android")
	s.observe(t0.Add(reportedOfflineHold+2*time.Second), 2, 1, "")
	require.True(t, s.snapshot().Online)
}

func TestOnlineModeOfflineIsImmediate(t *testing.T) {
	var s onlineState
	t0 := time.Unix(1000, 0)
	s.observe(t0, 0, 1, "")
	line, _ := s.observe(t0.Add(time.Second), 0, 0, visorapi.ModeOffline)
	require.Equal(t, "network: offline (network mode offline)", line)
}

// Leaving offline refreshes the network once, and a later move between
// dmsg-only and online does not.
func TestBackOnlineRefreshesOnce(t *testing.T) {
	fastNetMove(t)
	g := offlineGrace
	offlineGrace = 0
	t.Cleanup(func() { offlineGrace = g })
	v := &Visor{}
	var tps, ses atomic.Int64
	v.netMove.online.countTransports = func() int { return int(tps.Load()) }
	v.netMove.online.countSessions = func() int { return int(ses.Load()) }
	ran := make(chan string, 4)
	v.netMove.refresh = func(r string) { ran <- r }

	now := time.Now()
	v.checkOnline(now)
	require.Equal(t, visorapi.ConnOffline, v.Connectivity().State)
	ses.Store(2)
	for i := range 3 {
		v.checkOnline(now.Add(time.Duration(i+1) * time.Second))
	}
	require.Equal(t, visorapi.ConnDmsgOnly, v.Connectivity().State)
	tps.Store(1)
	for i := range 3 {
		v.checkOnline(now.Add(time.Duration(i+4) * time.Second))
	}
	require.Equal(t, netBackOnline, <-ran)
	select {
	case r := <-ran:
		t.Fatalf("second refresh %q", r)
	case <-time.After(100 * time.Millisecond):
	}
	c := v.NetworkState().Connectivity
	require.True(t, c.Online)
	require.Equal(t, 1, c.Transports)
	require.Equal(t, 2, c.DmsgSessions)
}

// Apps may report offline, lost and online alongside moved and changed.
func TestNetworkEventOfflineOnline(t *testing.T) {
	fastNetMove(t)
	v, _ := meterVisor(t)
	v.netMove.online.countTransports = func() int { return 1 }
	v.netMove.online.countSessions = func() int { return 1 }
	ran := make(chan string, 4)
	v.netMove.refresh = func(r string) { ran <- r }

	require.NoError(t, v.NetworkEvent(visorapi.NetworkEvent{Event: "changed"}))
	v.checkOnline(time.Now())
	require.True(t, v.Connectivity().Online)
	for _, ev := range []string{"offline", "lost"} {
		require.NoError(t, v.NetworkEvent(visorapi.NetworkEvent{Event: ev}))
		c := v.Connectivity()
		require.False(t, c.Online)
		require.Equal(t, "android reported no network", c.Reason)
	}
	require.NoError(t, v.NetworkEvent(visorapi.NetworkEvent{Event: "online"}))
	require.True(t, v.Connectivity().Online)
	got := <-ran
	require.Contains(t, got, "android online")
	require.Contains(t, got, netBackOnline)
}
