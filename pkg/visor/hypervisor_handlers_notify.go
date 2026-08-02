// Package visor pkg/visor/hypervisor_handlers_notify.go c3-vis-core
//
// The host-app sink of the notification hub (tier (b), see notifyhub.go): an
// SSE stream of the notifications apps publish, for a host application that can
// surface them natively — the Android foreground service posting through
// per-app notification channels, and later the manager UI's notification bell.
//
// Live-only, no replay: a subscriber sees what arrives while it is connected
// and nothing else. A reconnecting phone getting a burst of stale "you had a
// message" toasts is worse than missing them, and the hub's whole contract is
// best-effort delivery to whoever is listening now.
//
// While at least one subscriber is connected the hub stops posting to the
// host OS, so a desktop running the Android-style host app doesn't double up.
// That is derived per publish from the live subscriber set — never cached — so
// a dropped connection restores the desktop path immediately.
package visor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// notifyStreamCapacity is the per-subscriber buffer for the SSE stream.
// Notifications are sparse; a consumer this far behind has stopped reading.
const notifyStreamCapacity = 64

// notifyPingInterval keeps an idle stream alive and — more importantly — makes
// a vanished client observable. Once the write deadline is cleared a failed
// write is the only disconnect signal, and a notification feed can legitimately
// be silent for hours (the log stream never needed this because it is chatty).
const notifyPingInterval = 20 * time.Second

// notifyWriteTimeout bounds a single write to a subscriber. Generous — it is
// not a liveness heartbeat (that's notifyPingInterval) but a backstop that
// unwedges a handler blocked on a peer that stopped reading without closing.
const notifyWriteTimeout = 45 * time.Second

// notifyEvent is the wire shape of one SSE event. Declared explicitly rather
// than marshaling appserver.NotifyReq so the JSON contract the Android client
// codes against is visible here and can't drift with a Go field rename.
type notifyEvent struct {
	App   string `json:"app"`
	Title string `json:"title"`
	Body  string `json:"body"`
	Tag   string `json:"tag,omitempty"`
}

// getNotifyStream streams notifications published by this visor's apps as SSE.
func (hv *Hypervisor) getNotifyStream() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		if hv.visor == nil {
			http.Error(w, "notification hub unavailable", http.StatusServiceUnavailable)
			return
		}
		// Long-lived stream: the hypervisor http.Server's WriteTimeout (10s) is
		// a single deadline for the WHOLE response, so left alone it tears the
		// stream down ~10s in.
		//
		// Re-armed per write rather than cleared outright (which is what getLog
		// does). Clearing it leaves the handler with NO liveness bound: a peer
		// that keeps the socket open but stops reading — a dozing phone, a
		// zero-window TCP receiver — produces neither FIN nor RST, so the
		// request context never cancels and the goroutine parks forever inside
		// a write. It would then hold its subscription indefinitely, and since
		// subscriber presence suppresses the host-OS sink, the desktop would go
		// permanently silent. A rolling deadline turns that wedge into a write
		// error, which unwinds to the deferred cancel and restores tier (c).
		rc := http.NewResponseController(w)
		armDeadline := func() {
			if err := rc.SetWriteDeadline(time.Now().Add(notifyWriteTimeout)); err != nil {
				// Non-fatal: on a transport without deadline control the stream
				// still works, it just re-inherits the server WriteTimeout.
				hv.log(r).WithError(err).Debug("notify SSE: could not extend write deadline")
			}
		}
		armDeadline()

		ch, cancel := hv.visor.SubscribeNotifications(notifyStreamCapacity)
		if ch == nil {
			http.Error(w, "notification hub unavailable", http.StatusServiceUnavailable)
			return
		}
		// Unconditional: leaking a subscriber would suppress the host-OS sink
		// forever, silently muting the desktop.
		defer cancel()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Connection", "keep-alive")
		// Defeat proxy buffering, which would hold events until the buffer
		// fills — indistinguishable from "nothing happened" on a sparse feed.
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		ping := time.NewTicker(notifyPingInterval)
		defer ping.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-ping.C:
				armDeadline()
				if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
					return // client gone
				}
				flusher.Flush()
			case n, open := <-ch:
				if !open {
					return
				}
				b, err := json.Marshal(notifyEvent{
					App:   n.App,
					Title: n.Title,
					Body:  n.Body,
					Tag:   n.Tag,
				})
				if err != nil {
					continue
				}
				armDeadline()
				if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
					return // client gone
				}
				flusher.Flush()
			}
		}
	}
}
