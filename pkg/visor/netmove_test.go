package visor

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
)

func fastNetMove(t *testing.T) {
	t.Helper()
	d, g := netMoveDebounce, netMoveMinGap
	netMoveDebounce, netMoveMinGap = 20*time.Millisecond, 0
	t.Cleanup(func() { netMoveDebounce, netMoveMinGap = d, g })
}

// fakeMoveAR records what a network move hands the address resolver client.
type fakeMoveAR struct {
	addrresolver.APIClient
	mu     sync.Mutex
	ips    []string
	movedN int
}

func (f *fakeMoveAR) SetPublicIP(v4, v6 string) {
	f.mu.Lock()
	f.ips = append(f.ips, v4+"|"+v6)
	f.mu.Unlock()
}

func (f *fakeMoveAR) NetworkMoved() {
	f.mu.Lock()
	f.movedN++
	f.mu.Unlock()
}

func TestNetworkMovedCoalesces(t *testing.T) {
	fastNetMove(t)
	v := &Visor{}
	release := make(chan struct{})
	var mu sync.Mutex
	var reasons []string
	v.netMove.refresh = func(r string) {
		mu.Lock()
		reasons = append(reasons, r)
		first := len(reasons) == 1
		mu.Unlock()
		if first {
			<-release
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); v.networkMoved("burst") }()
	}
	wg.Wait()
	require.Eventually(t, func() bool { return v.netMove.runs.Load() == 1 }, time.Second, time.Millisecond)

	// Triggers during a run fold into exactly one more.
	for i := 0; i < 10; i++ {
		v.networkMoved("watcher")
		v.networkMoved("android")
	}
	close(release)
	require.Eventually(t, func() bool {
		v.netMove.mu.Lock()
		defer v.netMove.mu.Unlock()
		return !v.netMove.scheduled
	}, time.Second, time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	require.EqualValues(t, 2, v.netMove.runs.Load())
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"burst", "watcher, android"}, reasons)
}

// A determination reaches the AR client and every live public IP reader.
func TestApplyPublicIPReachesReaders(t *testing.T) {
	v := &Visor{}
	ar := &fakeMoveAR{}
	v.applyPublicIP(ar, publicIPResult{v4: "203.0.113.5", source: "dmsg"})
	v.applyPublicIP(ar, publicIPResult{v4: "198.51.100.7", v6: "2001:db8::7", source: "dmsg"})
	require.Equal(t, []string{"203.0.113.5|", "198.51.100.7|2001:db8::7"}, ar.ips)
	require.Equal(t, "198.51.100.7", v.currentPublicIP(), "the service discovery getter")
	st := v.NetworkState()
	require.Equal(t, "198.51.100.7", st.PublicIP)
	require.Equal(t, "2001:db8::7", st.PublicIPv6)
	require.Equal(t, "dmsg", st.PublicIPSource)
	require.False(t, st.PublicIPAt.IsZero())
}

// A move drops the visor's own card and AR view, tells the AR client, and
// the next card is built from the records the resolver now holds.
func TestRefreshNetworkRebuildsTheReachCard(t *testing.T) {
	v := reachTestVisor(t)
	ar := &fakeMoveAR{}
	v.initLock = new(sync.RWMutex)
	v.arClient = ar
	v.noteReachBind("stcpr", addrresolver.LocalAddresses{Port: "7773", Addresses: []string{"192.168.1.5"}})
	old, err := v.ReachCardBody()
	require.NoError(t, err)
	require.Contains(t, string(old), "203.0.113.7")

	v.refreshNetwork("test")
	require.Nil(t, v.reach.own)
	require.Nil(t, v.reach.binds)
	require.Greater(t, v.arSelf.age(), arSelfMaxAge)
	require.Equal(t, 1, ar.movedN)
	st := v.NetworkState()
	require.EqualValues(t, 1, st.Moves)
	require.Equal(t, "test", st.LastMoveReason)

	// The resolver now holds the new address; the card follows.
	v.arSelf.set("stcpr", addrresolver.VisorData{RemoteAddr: "198.51.100.7:7773"})
	v.noteReachBind("stcpr", addrresolver.LocalAddresses{Port: "7773", Addresses: []string{"10.0.0.9"}, PublicIP: "198.51.100.7"})
	body, err := v.ReachCardBody()
	require.NoError(t, err)
	require.Contains(t, string(body), "198.51.100.7")
	require.NotContains(t, string(body), "203.0.113.7")
	require.Equal(t, []string{"10.0.0.9"}, v.NetworkState().Advertised["stcpr"])
	var card addrresolver.ReachCard
	require.NoError(t, json.Unmarshal(body, &card))
	_, err = card.Verify(v.conf.PK)
	require.NoError(t, err)
}

func TestWatcherFiresOnFingerprintChange(t *testing.T) {
	fastNetMove(t)
	v := &Visor{}
	var fp atomic.Value
	fp.Store("192.168.1.5")
	v.netMove.fingerprint = func() string { return fp.Load().(string) }
	var got atomic.Value
	v.netMove.refresh = func(r string) { got.Store(r) }
	v.setNetworkIntervals(5*time.Millisecond, -1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go v.watchNetwork(ctx)

	time.Sleep(30 * time.Millisecond)
	require.Zero(t, v.netMove.runs.Load(), "an unchanged network is not a move")

	fp.Store("10.0.0.9")
	require.Eventually(t, func() bool { return v.netMove.runs.Load() == 1 }, time.Second, time.Millisecond)
	require.Equal(t, "addresses changed", got.Load())

	// Turning the watcher off takes effect live.
	v.setNetworkIntervals(-1, -1)
	time.Sleep(10 * time.Millisecond)
	fp.Store("172.16.0.3")
	time.Sleep(50 * time.Millisecond)
	require.EqualValues(t, 1, v.netMove.runs.Load())
	require.Equal(t, "off", v.NetworkState().WatchInterval)
}

func TestPublicIPMoved(t *testing.T) {
	ip := net.ParseIP
	for _, tc := range []struct {
		cur4, cur6 string
		v4, v6     net.IP
		moved      bool
	}{
		{"203.0.113.5", "", ip("203.0.113.5"), nil, false},
		{"203.0.113.5", "", ip("198.51.100.7"), nil, true},
		{"", "", ip("198.51.100.7"), nil, true},
		{"203.0.113.5", "", nil, nil, false},
		{"203.0.113.5", "", nil, ip("2001:db8::1"), false},
		{"203.0.113.5", "2001:db8::1", ip("203.0.113.5"), ip("2001:db8::2"), true},
	} {
		moved, why := publicIPMoved(tc.cur4, tc.cur6, tc.v4, tc.v6)
		require.Equalf(t, tc.moved, moved, "%+v", tc)
		require.Equal(t, moved, strings.HasPrefix(why, "public ip"))
	}
}

// eventSink is an app subscribed to network_moved.
type eventSink struct {
	got chan *appevent.Event
}

func (e *eventSink) Close() error { return nil }
func (e *eventSink) Hello() *appcommon.Hello {
	return &appcommon.Hello{EventSubs: map[string]bool{appevent.NetworkMoved: true}}
}
func (e *eventSink) Notify(_ context.Context, ev *appevent.Event) error {
	e.got <- ev
	return nil
}

// A handled move is announced to the apps, so the VPN client can move its
// direct routes onto the new gateway.
func TestRefreshNetworkTellsTheApps(t *testing.T) {
	v := reachTestVisor(t)
	v.initLock = new(sync.RWMutex)
	v.arClient = &fakeMoveAR{}
	sink := &eventSink{got: make(chan *appevent.Event, 1)}
	v.ebc = appevent.NewBroadcaster(logging.MustGetLogger("ebc"), time.Second)
	t.Cleanup(func() { _ = v.ebc.Close() }) //nolint:errcheck
	v.ebc.AddClient(sink)

	v.refreshNetwork("android")
	select {
	case ev := <-sink.got:
		require.Equal(t, appevent.NetworkMoved, ev.Type)
		var d appevent.NetworkMovedData
		ev.Unmarshal(&d)
		require.Equal(t, "android", d.Reason)
	case <-time.After(2 * time.Second):
		t.Fatal("the apps were not told of the move")
	}
}
