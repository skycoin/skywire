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

	line, back := s.observe(at(0), 0, "")
	require.Empty(t, line)
	require.False(t, back)
	require.Equal(t, visorapi.ConnConnecting, s.snapshot().State)

	// No session within the grace after start reads as offline.
	line, _ = s.observe(at(offlineGrace), 0, "")
	require.Equal(t, "network: offline (no dmsg sessions for 30s)", line)
	require.False(t, s.snapshot().Online)

	// The first session after offline is a return, reported once.
	line, back = s.observe(at(offlineGrace+time.Second), 2, "")
	require.Equal(t, "network: online (2 dmsg sessions)", line)
	require.True(t, back)
	line, back = s.observe(at(offlineGrace+2*time.Second), 3, "")
	require.Empty(t, line)
	require.False(t, back)
	require.Equal(t, "3 dmsg sessions", s.snapshot().Reason)

	// Losing the sessions waits out the grace before going offline.
	lost := offlineGrace + 2*time.Second
	line, _ = s.observe(at(lost+offlineGrace-time.Second), 0, "")
	require.Empty(t, line)
	require.True(t, s.snapshot().Online)
	line, _ = s.observe(at(lost+offlineGrace), 0, "")
	require.Contains(t, line, "offline")
	require.EqualValues(t, 3, s.snapshot().Changes)
}

func TestOnlineConnectingToOnlineIsNotAReturn(t *testing.T) {
	var s onlineState
	line, back := s.observe(time.Unix(1, 0), 1, "")
	require.Equal(t, "network: online (1 dmsg sessions)", line)
	require.False(t, back)
}

func TestOnlineReportedOfflineOutweighsSessions(t *testing.T) {
	var s onlineState
	t0 := time.Unix(1000, 0)
	s.observe(t0, 1, "")
	s.report(t0, true, "browser")
	line, _ := s.observe(t0.Add(time.Second), 1, "")
	require.Equal(t, "network: offline (browser reported no network)", line)
	// The report expires, and a later report of a network clears it.
	line, back := s.observe(t0.Add(reportedOfflineHold), 1, "")
	require.Contains(t, line, "online")
	require.True(t, back)
	s.report(t0.Add(reportedOfflineHold), true, "android")
	s.observe(t0.Add(reportedOfflineHold+time.Second), 1, "")
	require.False(t, s.snapshot().Online)
	s.report(t0.Add(reportedOfflineHold+2*time.Second), false, "android")
	s.observe(t0.Add(reportedOfflineHold+2*time.Second), 1, "")
	require.True(t, s.snapshot().Online)
}

func TestOnlineModeOfflineIsImmediate(t *testing.T) {
	var s onlineState
	t0 := time.Unix(1000, 0)
	s.observe(t0, 1, "")
	line, _ := s.observe(t0.Add(time.Second), 0, visorapi.ModeOffline)
	require.Equal(t, "network: offline (network mode offline)", line)
}

// A return from offline refreshes the network once, keeping the new sessions.
func TestBackOnlineRefreshesOnce(t *testing.T) {
	fastNetMove(t)
	g := offlineGrace
	offlineGrace = 0
	t.Cleanup(func() { offlineGrace = g })
	v := &Visor{}
	var n atomic.Int64
	v.netMove.online.sessions = func() int { return int(n.Load()) }
	ran := make(chan string, 4)
	v.netMove.refresh = func(r string) { ran <- r }

	now := time.Now()
	v.checkOnline(now)
	require.Equal(t, visorapi.ConnOffline, v.Connectivity().State)
	n.Store(2)
	for i := range 5 {
		v.checkOnline(now.Add(time.Duration(i+1) * time.Second))
	}
	require.Equal(t, netBackOnline, <-ran)
	select {
	case r := <-ran:
		t.Fatalf("second refresh %q", r)
	case <-time.After(100 * time.Millisecond):
	}
	require.True(t, v.NetworkState().Connectivity.Online)
}

// Apps may report offline, lost and online alongside moved and changed.
func TestNetworkEventOfflineOnline(t *testing.T) {
	fastNetMove(t)
	v, _ := meterVisor(t)
	v.netMove.online.sessions = func() int { return 1 }
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
