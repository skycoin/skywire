// Package visor — pkg/visor/notifyhub_test.go: pins the notification hub's
// priority chain, which is the whole point of the type. The chain (capable UI >
// host-app subscribers > host OS > drop) is what guarantees an event surfaces
// exactly ONCE, so every tier boundary gets a test: silence when a UI holds the
// lease, no host-OS post while a subscriber is attached, and — the regression
// that would silently mute a desktop — the host-OS path returning the moment
// the last subscriber goes away.
//
// The real host-OS sink is never invoked: both osnotify seams are replaced with
// recorders, so the suite posts no desktop notification on a developer machine.
package visor

import (
	"sync"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/app/appserver"
)

// osRecorder stands in for the host-OS sink (tier c).
type osRecorder struct {
	mu        sync.Mutex
	got       []appserver.NotifyReq
	available bool
	fired     chan struct{}
}

func newOSRecorder(available bool) *osRecorder {
	return &osRecorder{available: available, fired: make(chan struct{}, 16)}
}

func (o *osRecorder) isAvailable() bool { return o.available }

func (o *osRecorder) send(n appserver.NotifyReq) {
	o.mu.Lock()
	o.got = append(o.got, n)
	o.mu.Unlock()
	o.fired <- struct{}{}
}

func (o *osRecorder) calls() []appserver.NotifyReq {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]appserver.NotifyReq(nil), o.got...)
}

// awaitOS waits for the host-OS sink to fire, which happens on its own
// goroutine. Fails the test rather than hanging if it never does.
func (o *osRecorder) awaitOS(t *testing.T) {
	t.Helper()
	select {
	case <-o.fired:
	case <-time.After(2 * time.Second):
		t.Fatal("host-OS sink never fired")
	}
}

// refuteOS asserts the host-OS sink stays silent. The wait has to be long
// enough that a wrongly-spawned goroutine would have run.
func (o *osRecorder) refuteOS(t *testing.T) {
	t.Helper()
	select {
	case <-o.fired:
		t.Fatal("host-OS sink fired but a higher-priority tier should have claimed the event")
	case <-time.After(100 * time.Millisecond):
	}
}

// newTestHub builds a hub whose host-OS tier is a recorder.
func newTestHub(available bool) (*NotifyHub, *osRecorder) {
	rec := newOSRecorder(available)
	h := NewNotifyHub()
	h.osAvailable = rec.isAvailable
	h.osSend = rec.send
	return h, rec
}

func TestNotifyHub_OSSinkWhenNothingElseAttached(t *testing.T) {
	// The plain-desktop case: no UI lease, no subscriber → the host OS gets it.
	h, rec := newTestHub(true)

	h.Publish(appserver.NotifyReq{App: "skychat", Title: "alice", Body: "hi"})

	rec.awaitOS(t)
	got := rec.calls()
	if len(got) != 1 {
		t.Fatalf("host-OS sink calls = %d, want 1", len(got))
	}
	if got[0].Title != "alice" || got[0].Body != "hi" {
		t.Errorf("host-OS sink got %+v, want title=alice body=hi", got[0])
	}
}

func TestNotifyHub_CapableUISuppressesEverythingBelow(t *testing.T) {
	// Tier (a): a UI that will surface this itself owns the notification.
	// Publishing anyway is what would make an event appear twice.
	h, rec := newTestHub(true)
	ch, cancel := h.Subscribe(4)
	defer cancel()

	h.MarkUICapable(true)
	h.Publish(appserver.NotifyReq{App: "skychat", Title: "alice", Body: "hi"})

	rec.refuteOS(t)
	select {
	case n := <-ch:
		t.Fatalf("subscriber received %+v, but a capable UI outranks it", n)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestNotifyHub_SubscriberSuppressesOSSink(t *testing.T) {
	// Tier (b) outranks tier (c): a host app posts natively, so the visor must
	// not also raise a desktop toast for the same event.
	h, rec := newTestHub(true)
	ch, cancel := h.Subscribe(4)
	defer cancel()

	h.Publish(appserver.NotifyReq{App: "skychat", Title: "alice", Body: "hi"})

	select {
	case n := <-ch:
		if n.Title != "alice" {
			t.Errorf("subscriber got title %q, want alice", n.Title)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber never received the notification")
	}
	rec.refuteOS(t)
}

func TestNotifyHub_OSSinkReturnsAfterLastSubscriberLeaves(t *testing.T) {
	// The regression this guards: suppression is DERIVED per publish, never
	// cached. A stored "a host app is attached" flag plus a dropped phone
	// connection would mute the desktop permanently.
	h, rec := newTestHub(true)
	_, cancel := h.Subscribe(4)

	h.Publish(appserver.NotifyReq{App: "skychat", Title: "first"})
	rec.refuteOS(t)

	cancel()
	if got := h.SubscriberCount(); got != 0 {
		t.Fatalf("SubscriberCount after cancel = %d, want 0", got)
	}

	h.Publish(appserver.NotifyReq{App: "skychat", Title: "second"})
	rec.awaitOS(t)
	got := rec.calls()
	if len(got) != 1 || got[0].Title != "second" {
		t.Fatalf("host-OS sink calls = %+v, want exactly the post-cancel one", got)
	}
}

func TestNotifyHub_LeaseExpiryRestoresLowerTiers(t *testing.T) {
	// The lease is time-bounded so a UI that dies without clearing it can't
	// silence notifications forever.
	h, rec := newTestHub(true)

	h.MarkUICapable(true)
	if !h.uiCapable() {
		t.Fatal("lease should be fresh immediately after MarkUICapable(true)")
	}

	// Backdate past the TTL rather than sleeping for it.
	h.leaseMu.Lock()
	h.leaseAt = time.Now().Add(-notifyCapableTTL - time.Second)
	h.leaseMu.Unlock()

	if h.uiCapable() {
		t.Fatal("lease should be stale once older than notifyCapableTTL")
	}
	h.Publish(appserver.NotifyReq{App: "skychat", Title: "after expiry"})
	rec.awaitOS(t)
}

func TestNotifyHub_MarkUICapableFalseClearsLease(t *testing.T) {
	h, rec := newTestHub(true)

	h.MarkUICapable(true)
	h.MarkUICapable(false)

	if h.uiCapable() {
		t.Fatal("lease should be cleared by MarkUICapable(false)")
	}
	h.Publish(appserver.NotifyReq{App: "skychat", Title: "x"})
	rec.awaitOS(t)
}

func TestNotifyHub_HeadlessDropsSilently(t *testing.T) {
	// The common fleet case: server/router visor, no UI, no subscriber, no
	// desktop session. Dropping is the expected outcome, not an error.
	h, rec := newTestHub(false)

	h.Publish(appserver.NotifyReq{App: "skychat", Title: "nobody home"})

	rec.refuteOS(t)
	if got := len(rec.calls()); got != 0 {
		t.Errorf("host-OS sink calls = %d, want 0 when unavailable", got)
	}
}

func TestNotifyHub_SlowSubscriberDropsRatherThanBlocks(t *testing.T) {
	// A consumer that stops reading must lose events, never back-pressure the
	// publisher — Publish runs on the app's RPC serve goroutine.
	h, _ := newTestHub(true)
	_, cancel := h.Subscribe(1)

	for i := 0; i < 5; i++ {
		h.Publish(appserver.NotifyReq{App: "skychat", Title: "x"})
	}

	// One fits the buf=1 channel; the other four drop.
	if dropped := cancel(); dropped != 4 {
		t.Errorf("dropped = %d, want 4", dropped)
	}
}

func TestNotifyHub_CancelIsIdempotent(t *testing.T) {
	// The SSE handler defers cancel; a double call must not close twice.
	h, _ := newTestHub(true)
	_, cancel := h.Subscribe(2)

	h.Publish(appserver.NotifyReq{App: "skychat", Title: "x"})
	first := cancel()
	second := cancel()

	if first != second {
		t.Errorf("second cancel returned %d, want the same %d", second, first)
	}
	if got := h.SubscriberCount(); got != 0 {
		t.Errorf("SubscriberCount = %d after double cancel, want 0", got)
	}
}

func TestNotifyHub_FanoutReachesEverySubscriber(t *testing.T) {
	h, _ := newTestHub(true)
	const subs = 3

	chans := make([]<-chan appserver.NotifyReq, subs)
	for i := 0; i < subs; i++ {
		ch, cancel := h.Subscribe(4)
		defer cancel()
		chans[i] = ch
	}
	if got := h.SubscriberCount(); got != subs {
		t.Fatalf("SubscriberCount = %d, want %d", got, subs)
	}

	h.Publish(appserver.NotifyReq{App: "skychat", Title: "broadcast"})

	for i, ch := range chans {
		select {
		case n := <-ch:
			if n.Title != "broadcast" {
				t.Errorf("subscriber %d got title %q, want broadcast", i, n.Title)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("subscriber %d never received the notification", i)
		}
	}
}

func TestNotifyHub_ConcurrentPublishSubscribeCancel(t *testing.T) {
	// Race detector coverage for the one genuinely dangerous interaction:
	// cancel deletes+closes a subscriber channel while a fan-out may be
	// mid-iteration. Without the write-lock-before-close discipline this is a
	// send-on-closed-channel panic.
	h, _ := newTestHub(false) // no OS sink goroutines to leak
	const workers = 16

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				h.Publish(appserver.NotifyReq{App: "skychat", Title: "x"})
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				ch, cancel := h.Subscribe(1)
				select {
				case <-ch:
				default:
				}
				cancel()
			}
		}()
	}
	wg.Wait()

	if got := h.SubscriberCount(); got != 0 {
		t.Errorf("SubscriberCount = %d after all cancels, want 0", got)
	}
}

func TestAppDisplayName(t *testing.T) {
	// The label a notification center shows. skychat used to pass "Skychat"
	// itself; now that the hub owns it, the visible name must not drift to the
	// lowercase app id.
	cases := []struct {
		app  string
		want string
	}{
		{"skychat", "Skychat"},
		{"vpn-client", "SkyVPN"},
		{"skysocks-client", "Skysocks"},
		{"skydex-client", "SkyDEX"},
		{"", "Skywire"},
		{"some-future-app", "some-future-app"},
	}
	for _, c := range cases {
		if got := appDisplayName(c.app); got != c.want {
			t.Errorf("appDisplayName(%q) = %q, want %q", c.app, got, c.want)
		}
	}
}

func TestVisor_SubscribeNotifications_NilSafe(t *testing.T) {
	// Bare &Visor{} fixtures are common in this package; the accessor must
	// answer "unavailable" rather than panic so the SSE handler can 503.
	var v *Visor
	ch, cancel := v.SubscribeNotifications(1)
	if ch != nil {
		t.Error("nil visor should yield a nil channel")
	}
	if got := cancel(); got != 0 {
		t.Errorf("nil-visor cancel returned %d, want 0", got)
	}

	empty := &Visor{}
	if ch, _ := empty.SubscribeNotifications(1); ch != nil {
		t.Error("visor without a hub should yield a nil channel")
	}
	if empty.NotifyHub() != nil {
		t.Error("visor without a hub should report a nil hub")
	}
}
