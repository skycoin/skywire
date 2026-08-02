// Package visor pkg/visor/notifyhub.go c3-vis-core
//
// The visor-global notification hub — the "where" half of the notification
// system whose "whether" half lives in each app (see pkg/app/appserver/notify.go).
//
// An app publishes a NotifyReq and stops caring. The hub owns the one decision
// an app cannot make for itself: which delivery path can actually reach the
// user right now. It walks a fixed priority chain and stops at the first tier
// that can deliver, so a single event surfaces exactly once:
//
//	(a) an attached UI holding a fresh capability lease — a UI that will really
//	    put this in front of the user (it also knows what is focused and muted),
//	    so the hub must not double up. No consumer ships today: the lease is the
//	    designed-in slot for the hypervisor manager UI's notification bell, and
//	    it stays false until something calls MarkUICapable.
//	(b) subscribed host apps — the Android foreground service (and later the
//	    manager UI) reading the SSE stream, which post native notifications
//	    through per-app channels.
//	(c) the host OS notification center via pkg/osnotify — the desktop default.
//	(d) nothing: on a headless visor (server, router, daemon) there is no UI, no
//	    subscriber and no desktop session, so the event drops. That is the
//	    normal case for most of the fleet, not an error.
//
// Why the tiers are ordered this way: each one is strictly "closer" to a user
// who is actually looking. A capable UI beats a background service beats a
// desktop toast. Suppression is always DERIVED at publish time (is anyone
// subscribed right now?), never stored — a cached "phone is attached" flag plus
// a dropped connection would mute the desktop permanently.
package visor

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/osnotify"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// notifyCapableTTL is how long a UI's "I can notify" heartbeat keeps the
// capability lease fresh. A consumer refreshes well within this window; once it
// stops (window closed, notifications blocked, app backgrounded) the lease goes
// stale and the hub resumes delivering through the lower tiers.
//
// Matches skychat's app-local lease (cmd/apps/skychat/commands/osnotify.go) —
// this is that mechanism generalised from one app to the whole visor.
const notifyCapableTTL = 45 * time.Second

// notifySubCapacity is the default per-subscriber buffer. Notifications are
// sparse compared with log lines, so a small buffer is plenty; anything beyond
// it means the consumer has stopped reading and dropping is correct.
const notifySubCapacity = 64

// NotifyHub fans user-facing notifications out to whichever sink can reach the
// user. Safe for concurrent use; every method is non-blocking.
type NotifyHub struct {
	mu   sync.RWMutex
	subs map[*notifySub]struct{}

	// leaseMu guards the attached-UI capability lease. Separate from mu so a
	// heartbeat can never contend with a fan-out.
	leaseMu sync.Mutex
	leaseAt time.Time

	// Tier-(c) host-OS sink, split so the cheap availability probe can run
	// inline while the delivery (which shells out to a subprocess) is
	// spawned. Both are injected so tests never post a real notification.
	osAvailable func() bool
	osSend      func(n appserver.NotifyReq)

	// log traces which tier took each notification. Every outcome here is
	// silent by design — a dropped event on a headless visor is normal, and
	// the OS sink's errors are not actionable — which leaves an operator
	// asking "why did nothing appear?" with nothing to read. One Debug line
	// per publish answers it. Nil is valid (tests, a bare hub).
	log logrus.FieldLogger
}

type notifySub struct {
	ch chan appserver.NotifyReq
	// atomic.Uint64 rather than a bare uint64: on 32-bit targets (the 386 /
	// arm / armhf builds this repo ships) a bare uint64 following the channel
	// pointer lands at offset 4 and every atomic.AddUint64 on it panics with
	// "unaligned 64-bit atomic operation". atomic.Uint64 carries its own
	// alignment guarantee, so the field order stops mattering.
	dropped atomic.Uint64
	closed  atomic.Bool
}

// Compile-time guard for the alignment note above: if `dropped` ever reverts to
// a bare uint64 its offset stops being a multiple of 8 on 32-bit targets and
// this expression underflows, failing the build for that GOARCH. Far better
// than a panic on a router in the field, which is the only other way this
// surfaces (lint and the amd64 test suite both stay silent).
const _ = uint(0 - unsafe.Offsetof(notifySub{}.dropped)%8)

// NewNotifyHub constructs an empty hub with the host-OS sink wired up. log may
// be nil; it only carries the per-publish trace.
func NewNotifyHub(log logrus.FieldLogger) *NotifyHub {
	h := &NotifyHub{
		subs:        make(map[*notifySub]struct{}),
		osAvailable: osnotify.Available,
		log:         log,
	}
	h.osSend = h.sendToOS
	return h
}

// trace records the routing decision for one notification. Never the body —
// that is routinely untrusted peer text — and not the title either, which is
// app-controlled and can carry the same. The app name and the tier are what
// answer "why didn't I get a notification?".
func (h *NotifyHub) trace(app, outcome string) {
	if h == nil || h.log == nil {
		return
	}
	h.log.WithField("app", app).WithField("sink", outcome).Debug("notification routed")
}

// Publish routes n to the first tier that can deliver it (see the file header).
// Never blocks: it runs on the publishing app's single RPC serve goroutine, so
// stalling here would stall that app's Dial/Write/Read too.
func (h *NotifyHub) Publish(n appserver.NotifyReq) {
	// (a) A UI that can surface this itself owns the notification — staying
	// quiet here is what stops an event appearing twice.
	if h.uiCapable() {
		h.trace(n.App, "attached-ui")
		return
	}

	// (b) Host-app subscribers (the Android service). Non-blocking per
	// subscriber: a consumer that has stopped reading loses events rather than
	// back-pressuring the publisher.
	if h.fanout(n) {
		h.trace(n.App, "subscriber")
		return
	}

	// (c) The host OS notification center. The availability probe is cached
	// and cheap, so it runs inline; the delivery shells out to a subprocess
	// and so gets its own goroutine.
	if h.osAvailable != nil && h.osSend != nil && h.osAvailable() {
		h.trace(n.App, "host-os")
		go h.osSend(n)
		return
	}

	// (d) Headless: nowhere to put it. Dropping is the expected outcome on
	// most of the fleet, so this is not an error — but it IS the answer when
	// someone asks why no notification appeared, so it gets a line too.
	h.trace(n.App, "dropped-headless")
}

// fanout delivers n to every current subscriber, reporting whether there was at
// least one. Zero subscribers is a fall-through to the next tier, NOT a drop —
// a divergence from skychat's SSE hub, which counts a no-subscriber broadcast
// as a drop because it is the only delivery path it has.
func (h *NotifyHub) fanout(n appserver.NotifyReq) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for sub := range h.subs {
		select {
		case sub.ch <- n:
		default:
			sub.dropped.Add(1)
		}
	}
	return len(h.subs) > 0
}

// Subscribe returns a receive-only channel of notifications plus a cancel func
// that closes it and reports how many events were dropped for this subscriber.
// Cancel is idempotent and MUST be called (the SSE handler defers it) or the
// subscriber leaks and keeps suppressing the host-OS sink forever.
func (h *NotifyHub) Subscribe(capacity int) (<-chan appserver.NotifyReq, func() uint64) {
	if capacity <= 0 {
		capacity = notifySubCapacity
	}
	sub := &notifySub{ch: make(chan appserver.NotifyReq, capacity)}

	h.mu.Lock()
	h.subs[sub] = struct{}{}
	h.mu.Unlock()

	cancel := func() uint64 {
		if !sub.closed.CompareAndSwap(false, true) {
			return sub.dropped.Load()
		}
		// Take the write lock so any in-flight fanout holding RLock has
		// finished iterating before we delete and close — otherwise this
		// races into a send on a closed channel.
		h.mu.Lock()
		delete(h.subs, sub)
		h.mu.Unlock()
		close(sub.ch)
		return sub.dropped.Load()
	}
	return sub.ch, cancel
}

// SubscriberCount reports how many host apps are currently subscribed.
func (h *NotifyHub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return len(h.subs)
}

// MarkUICapable refreshes (capable) or clears (!capable) the attached-UI
// capability lease. Nothing calls it yet — it is the seam the hypervisor
// manager UI's notification bell will heartbeat on, mirroring skychat's
// POST /notify-capable.
//
// The lease means "a UI that will ACTUALLY surface this to the user is
// attached", not merely "a UI is connected": a backgrounded mobile WebView can
// show nothing, so it must stop heartbeating or it would silence the native
// path (01_playbook.md Step 4a).
func (h *NotifyHub) MarkUICapable(capable bool) {
	h.leaseMu.Lock()
	defer h.leaseMu.Unlock()

	if capable {
		h.leaseAt = time.Now()
		return
	}
	h.leaseAt = time.Time{}
}

// uiCapable reports whether the capability lease is currently fresh.
func (h *NotifyHub) uiCapable() bool {
	h.leaseMu.Lock()
	defer h.leaseMu.Unlock()

	return !h.leaseAt.IsZero() && time.Since(h.leaseAt) < notifyCapableTTL
}

// sendToOS posts n to the host OS notification center. Best-effort: a failure
// is not actionable and must not propagate, but it IS logged at Debug — this is
// the last step before the user's screen, so "the notifier itself refused" and
// "we never got here" have to be distinguishable when someone reports a missing
// notification. ErrUnavailable is expected on a headless box and stays quiet.
//
// Tag is dropped: osnotify.Notification has no equivalent — coalescing is a
// concept only the host-app sinks (Android notification tags) support.
func (h *NotifyHub) sendToOS(n appserver.NotifyReq) {
	err := osnotify.Notify(osnotify.Notification{
		Title: n.Title,
		Body:  n.Body,
		// Keeps the visible label stable now that apps no longer pass it
		// themselves — skychat used to send "Skychat", and without this its
		// notifications would silently start reading "skychat". Shared with
		// the `hv notify` bridge, which posts the same events elsewhere.
		AppName: skyenv.AppDisplayName(n.App),
	})
	if err != nil && !errors.Is(err, osnotify.ErrUnavailable) && h != nil && h.log != nil {
		// No body/title: the notifier's error text is ours, the payload is not.
		h.log.WithError(err).WithField("app", n.App).Debug("host-OS notification failed")
	}
}

// NotifyHub exposes the visor's notification hub. Nil-safe: a bare &Visor{}
// (test fixtures) has no hub.
func (v *Visor) NotifyHub() *NotifyHub {
	if v == nil {
		return nil
	}
	return v.notifyHub
}

// SubscribeNotifications registers a host-app subscriber on the visor's hub,
// mirroring SubscribeLogs. Returns a nil channel when there is no hub so
// callers can answer "unavailable" rather than panic.
func (v *Visor) SubscribeNotifications(capacity int) (<-chan appserver.NotifyReq, func() uint64) {
	if v == nil || v.notifyHub == nil {
		return nil, func() uint64 { return 0 }
	}
	return v.notifyHub.Subscribe(capacity)
}
