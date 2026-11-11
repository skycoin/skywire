package visor

import (
	"context"
	"net"
	"net/rpc"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gobrpc "github.com/0magnet/gobrpc"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/alerts"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// linkPair is an in-process visor and hypervisor joined the way
// ServeRPCClient joins them: the visor serves RPC on a conn the
// hypervisor holds as a client.
type linkPair struct {
	hv     *Hypervisor
	pk     cipher.PubKey
	link   *hvLink
	api    visorapi.API
	served chan struct{}
}

func newLinkPair(t *testing.T) *linkPair {
	t.Helper()
	hv := newKnownTestHV(t.TempDir())
	pk, _ := cipher.GenerateKeyPair()
	v := alertsTestVisor(t)
	link := newHVLink()
	srv := gobrpc.NewServer()
	require.NoError(t, registerVisorRPC(srv, visorapi.RPCPrefix, &RPC{visor: v, log: logging.New(), link: link}))
	sc, cc := net.Pipe()
	served := make(chan struct{})
	go func() {
		srv.ServeConn(sc)
		close(served)
	}()
	link.serving(served)
	api := visorapi.NewRPCClient(nil, cc, visorapi.RPCPrefix, 30*time.Second)
	hv.remoteVisors[pk] = Conn{API: api}
	hv.summaryCache[pk] = cachedSummary{sum: knownVisorSummary(knownVisor{PK: pk}), seenAt: time.Now()}
	return &linkPair{hv: hv, pk: pk, link: link, api: api, served: served}
}

// waitChecked waits for the call backs checkLinks started.
func waitChecked(t *testing.T, hv *Hypervisor, pk cipher.PubKey) {
	t.Helper()
	require.Eventually(t, func() bool {
		hv.links.mu.Lock()
		defer hv.links.mu.Unlock()
		l := hv.links.m[pk]
		return l != nil && !l.checking
	}, 5*time.Second, 5*time.Millisecond)
}

// observe feeds the hypervisor's targets to a tracker for minutes of
// checks every 30s from start, and returns every event.
func observe(tr *alerts.Tracker, hv *Hypervisor, start time.Time, minutes int, delay time.Duration) []alerts.Event {
	var evs []alerts.Event
	for i := 0; i <= minutes*2; i++ {
		now := start.Add(time.Duration(i) * 30 * time.Second)
		evs = append(evs, tr.Observe(now, delay, hv.alertTargets(now))...)
	}
	return evs
}

func TestGoodbyeOnRemoveIsDetachedNoAlert(t *testing.T) {
	p := newLinkPair(t)
	go p.hv.watchGoodbye(p.pk, p.api)
	require.Eventually(t, p.link.asked.Load, 5*time.Second, 5*time.Millisecond, "the hypervisor polls for a goodbye")

	start := time.Now()
	p.link.goodbye(byeDetached)
	require.Less(t, time.Since(start), hvByeWait, "the hypervisor took the goodbye and closed the link")
	<-p.served

	now := time.Now()
	require.Equal(t, alerts.StateDetached, p.hv.visorState(p.pk, now))
	p.hv.mu.RLock()
	_, connected := p.hv.remoteVisors[p.pk]
	p.hv.mu.RUnlock()
	require.False(t, connected)

	called := atomic.Int32{}
	p.hv.dialBack = func(context.Context, cipher.PubKey) dialBackResult { called.Add(1); return dialNoAnswer }
	p.hv.checkLinks(context.Background(), now)
	require.Zero(t, called.Load(), "a detached visor is not called back")

	tr := alerts.NewTracker(nil)
	require.Empty(t, observe(tr, p.hv, now, 30, time.Minute))
	require.NoError(t, p.hv.ForgetVisor(p.pk), "a detached visor can be forgotten")
}

func TestGoodbyeRestartingMutesForOneDelay(t *testing.T) {
	p := newLinkPair(t)
	go p.hv.watchGoodbye(p.pk, p.api)
	require.Eventually(t, p.link.asked.Load, 5*time.Second, 5*time.Millisecond)
	p.link.goodbye(byeRestarting)
	<-p.served

	delay := alerts.DefaultOfflineAfter // the hypervisor's delay without an alert service
	p.hv.dialBack = func(context.Context, cipher.PubKey) dialBackResult { return dialNoAnswer }
	now := time.Now()
	require.Equal(t, alerts.StateRestarting, p.hv.visorState(p.pk, now))
	tr := alerts.NewTracker(nil)
	require.Empty(t, tr.Observe(now, delay, p.hv.alertTargets(now)))
	require.Empty(t, tr.Observe(now.Add(delay-time.Second), delay, p.hv.alertTargets(now.Add(delay-time.Second))))

	// Past the grace the visor is called back like any other.
	later := now.Add(delay + time.Second)
	p.hv.checkLinks(context.Background(), later)
	waitChecked(t, p.hv, p.pk)
	require.Equal(t, alerts.StateOffline, p.hv.visorState(p.pk, later))
	require.Empty(t, tr.Observe(later, delay, p.hv.alertTargets(later)), "the offline delay starts after the grace")
	evs := tr.Observe(later.Add(delay), delay, p.hv.alertTargets(later.Add(delay)))
	require.Len(t, evs, 1)
	require.Equal(t, alerts.KindOffline, evs[0].Kind)
}

func TestLinkDropWithoutGoodbyeCallsBack(t *testing.T) {
	cases := []struct {
		name string
		res  []dialBackResult
		want string
	}{
		{"answers and lists us", []dialBackResult{dialListsUs}, alerts.StateReconnecting},
		{"answers without us", []dialBackResult{dialDetached}, alerts.StateDetached},
		{"up without RPC", []dialBackResult{dialUnverified}, alerts.StateReconnecting},
		{"no answer", []dialBackResult{dialNoAnswer}, alerts.StateOffline},
		{"one miss after answering", []dialBackResult{dialListsUs, dialNoAnswer}, alerts.StateReconnecting},
		{"two misses after answering", []dialBackResult{dialListsUs, dialNoAnswer, dialNoAnswer}, alerts.StateOffline},
		{"answers again", []dialBackResult{dialNoAnswer, dialListsUs}, alerts.StateReconnecting},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hv := newKnownTestHV(t.TempDir())
			pk, _ := cipher.GenerateKeyPair()
			now := time.Now()
			hv.summaryCache[pk] = cachedSummary{sum: knownVisorSummary(knownVisor{PK: pk}), seenAt: now.Add(-time.Minute)}
			require.Equal(t, alerts.StateOffline, hv.visorState(pk, now), "not yet called back")
			for _, r := range tc.res {
				hv.dialBack = func(context.Context, cipher.PubKey) dialBackResult { return r }
				hv.checkLinks(context.Background(), now)
				waitChecked(t, hv, pk)
			}
			require.Equal(t, tc.want, hv.visorState(pk, now))
		})
	}
}

func TestReconnectingAlertsOnlyPastDelay(t *testing.T) {
	hv := newKnownTestHV(t.TempDir())
	pk, _ := cipher.GenerateKeyPair()
	now := time.Now()
	hv.summaryCache[pk] = cachedSummary{sum: knownVisorSummary(knownVisor{PK: pk}), seenAt: now}
	hv.dialBack = func(context.Context, cipher.PubKey) dialBackResult { return dialListsUs }
	hv.checkLinks(context.Background(), now)
	waitChecked(t, hv, pk)

	delay := time.Minute
	tr := alerts.NewTracker(nil)
	require.Empty(t, tr.Observe(now, delay, hv.alertTargets(now)))
	require.Empty(t, tr.Observe(now.Add(30*time.Second), delay, hv.alertTargets(now.Add(30*time.Second))), "reconnecting within the delay")
	evs := tr.Observe(now.Add(delay), delay, hv.alertTargets(now.Add(delay)))
	require.Len(t, evs, 1)
	require.Equal(t, alerts.KindLinkLost, evs[0].Kind, "a distinct alert, not offline")
}

func TestVisorStopsAnsweringAlertsAfterDelay(t *testing.T) {
	p := newLinkPair(t)
	go p.hv.watchGoodbye(p.pk, p.api)
	require.Eventually(t, p.link.asked.Load, 5*time.Second, 5*time.Millisecond)
	start := time.Now()
	delay := time.Minute
	tr := alerts.NewTracker(nil)
	require.Empty(t, tr.Observe(start, delay, p.hv.alertTargets(start)), "online")

	// The visor dies: the link breaks with no goodbye and nothing answers.
	require.NoError(t, p.api.Close())
	p.hv.mu.Lock()
	delete(p.hv.remoteVisors, p.pk)
	p.hv.mu.Unlock()
	p.hv.dialBack = func(context.Context, cipher.PubKey) dialBackResult { return dialNoAnswer }
	at := start.Add(30 * time.Second)
	p.hv.checkLinks(context.Background(), at)
	waitChecked(t, p.hv, p.pk)
	require.Equal(t, alerts.StateOffline, p.hv.visorState(p.pk, at))
	require.Empty(t, tr.Observe(at, delay, p.hv.alertTargets(at)))
	evs := tr.Observe(start.Add(delay+time.Second), delay, p.hv.alertTargets(start.Add(delay+time.Second)))
	require.Len(t, evs, 1)
	require.Equal(t, alerts.KindOffline, evs[0].Kind)
}

// oldVisor is an RPC server from before HypervisorNotice.
type oldVisor struct{}

func (oldVisor) Overview(_ *struct{}, out *visorapi.Overview) error { return nil }

func TestOldVisorWithoutGoodbye(t *testing.T) {
	hv := newKnownTestHV(t.TempDir())
	pk, _ := cipher.GenerateKeyPair()
	srv := rpc.NewServer()
	require.NoError(t, srv.RegisterName(visorapi.RPCPrefix, oldVisor{}))
	sc, cc := net.Pipe()
	go srv.ServeConn(sc)
	api := visorapi.NewRPCClient(nil, cc, visorapi.RPCPrefix, 30*time.Second)
	hv.remoteVisors[pk] = Conn{API: api}

	done := make(chan struct{})
	go func() {
		hv.watchGoodbye(pk, api)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the watcher should give up on a visor without HypervisorNotice")
	}
	require.Equal(t, alerts.StateOnline, hv.visorState(pk, time.Now()), "the link stays up")
}

func TestGoodbyeToOldHypervisorDoesNotWait(t *testing.T) {
	l := newHVLink()
	served := make(chan struct{})
	l.serving(served)
	start := time.Now()
	l.goodbye(byeDetached)
	require.Less(t, time.Since(start), 100*time.Millisecond, "nobody asked, nothing to wait for")
	require.True(t, l.said())
	require.Equal(t, byeDetached, l.notice(time.Second))
	l.goodbye(byeRestarting)
	require.Equal(t, byeDetached, l.notice(time.Second), "the first reason stands")
}

func TestRemoveHypervisorSaysGoodbye(t *testing.T) {
	hvPK, _ := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()
	pk, _ := cipher.GenerateKeyPair()
	v := &Visor{
		conf:              &visorconfig.V1{Common: &visorconfig.Common{PK: pk}},
		initLock:          new(sync.RWMutex),
		hypervisorCancels: map[cipher.PubKey]context.CancelFunc{},
	}
	a, b := newHVLink(), newHVLink()
	v.setHVLink(hvPK, a)
	v.setHVLink(other, b)
	canceled := false
	v.hypervisorCancels[hvPK] = func() {
		require.True(t, a.said(), "the goodbye goes before the disconnect")
		canceled = true
	}
	require.NoError(t, v.RemoveHypervisor(hvPK))
	require.True(t, canceled)
	require.Equal(t, byeDetached, a.notice(time.Second))
	require.False(t, b.said(), "only the removed hypervisor is told")

	_, err := v.RemoveAllHypervisors()
	require.NoError(t, err)
	require.Equal(t, byeDetached, b.notice(time.Second))
}

func TestShutdownSaysRestarting(t *testing.T) {
	hvPK, _ := cipher.GenerateKeyPair()
	common, err := visorconfig.NewCommon(nil, "", nil)
	require.NoError(t, err)
	v := &Visor{conf: &visorconfig.V1{Common: common}, initLock: new(sync.RWMutex)}
	l := newHVLink()
	v.setHVLink(hvPK, l)
	require.NoError(t, v.Close())
	require.Equal(t, byeRestarting, l.notice(time.Second))
}

func TestOneStateEverywhere(t *testing.T) {
	hv := newKnownTestHV(t.TempDir())
	now := time.Now()
	var pks []cipher.PubKey
	answer := map[cipher.PubKey]dialBackResult{}
	for _, res := range []dialBackResult{dialListsUs, dialDetached, dialNoAnswer} {
		pk, _ := cipher.GenerateKeyPair()
		pks = append(pks, pk)
		answer[pk] = res
		// Seen a minute ago: hv ls used to call this online for 3 minutes.
		hv.summaryCache[pk] = cachedSummary{sum: knownVisorSummary(knownVisor{PK: pk, Version: "1"}), seenAt: now.Add(-time.Minute)}
	}
	hv.dialBack = func(_ context.Context, pk cipher.PubKey) dialBackResult { return answer[pk] }
	hv.checkLinks(context.Background(), now)
	for _, pk := range pks {
		waitChecked(t, hv, pk)
	}
	want := map[cipher.PubKey]string{pks[0]: alerts.StateReconnecting, pks[1]: alerts.StateDetached, pks[2]: alerts.StateOffline}
	for _, e := range hv.offlineKnownEntries(nil) {
		require.Equal(t, want[e.PK], e.State, "hv ls")
		require.False(t, e.Online)
	}
	for _, tg := range hv.alertTargets(now) {
		require.Equal(t, want[tg.PK], tg.State, "alerts")
	}
}

// overviewVisor answers Overview with a fixed hypervisor list.
type overviewVisor struct{ hvs []cipher.PubKey }

func (o overviewVisor) Overview(_ *struct{}, out *visorapi.Overview) error {
	out.Hypervisors = o.hvs
	return nil
}

func TestAskHypervisors(t *testing.T) {
	self, _ := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()
	ask := func(hvs []cipher.PubKey) dialBackResult {
		srv := rpc.NewServer()
		require.NoError(t, srv.RegisterName(visorapi.RPCPrefix, overviewVisor{hvs}))
		sc, cc := net.Pipe()
		go srv.ServeConn(sc)
		return askHypervisors(cc, self)
	}
	require.Equal(t, dialListsUs, ask([]cipher.PubKey{other, self}))
	require.Equal(t, dialDetached, ask([]cipher.PubKey{other}))

	// A visor that no longer trusts us accepts the stream and closes it.
	sc, cc := net.Pipe()
	go func() { _ = sc.Close() }() //nolint:errcheck
	require.Equal(t, dialDetached, askHypervisors(cc, self))
}
