// Package visor pkg/visor/hypervisor_handlers_log.go c3-vis-core
// Events stream of the local visor's live log, so the HV-UI log window can show
// the NATIVE visor's server-side log (the wasm visor logs to the browser console
// and is captured there directly; the native visor's log lives server-side).
package visor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/logging"
)

// getLog streams the local visor's live log entries as SSE. Each event is a JSON
// {level, time, msg} the browse.js log window feeds into window.skywireLog. The
// broadcaster never blocks on a slow consumer (it drops on a full buffer), so a
// stalled client can't back-pressure the visor's logging.
func (hv *Hypervisor) getLog() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		// This is a long-lived stream. The hypervisor http.Server sets
		// WriteTimeout (10s), which is a single deadline for the WHOLE
		// response — for an endless SSE stream it fires ~10s in and forcibly
		// closes the connection, which the browser surfaces as a recurring
		// ERR_INCOMPLETE_CHUNKED_ENCODING and a 10s-cyclic gap in the log
		// window. Clear the write deadline for THIS response only (via
		// ResponseController); every other endpoint keeps WriteTimeout.
		if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
			// Non-fatal: on a transport without deadline control the stream
			// still works, it just re-inherits the server WriteTimeout.
			hv.log(r).WithError(err).Debug("log SSE: could not clear write deadline")
		}
		// Empty filter = firehose (every module, Debug+). See Broadcaster.matches.
		ch, cancel := hv.visor.SubscribeLogs(logging.Filter{MinLevel: logrus.DebugLevel}, 512)
		if ch == nil {
			http.Error(w, "log broadcaster unavailable", http.StatusServiceUnavailable)
			return
		}
		defer cancel()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		for {
			select {
			case <-r.Context().Done():
				return
			case e, open := <-ch:
				if !open {
					return
				}
				b, err := json.Marshal(map[string]interface{}{
					"level": e.Level.String(),
					"time":  e.Time.UnixMilli(),
					"msg":   formatLogEntry(e),
				})
				if err != nil {
					continue
				}
				if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
					return // client gone
				}
				flusher.Flush()
			}
		}
	}
}

// formatLogEntry renders a logrus entry as "[module] message k=v …" (module
// first, then the message, then the remaining fields sorted for stability) —
// matching how the console-captured wasm log reads.
func formatLogEntry(e *logrus.Entry) string {
	var b strings.Builder
	if m, ok := e.Data["_module"].(string); ok && m != "" {
		fmt.Fprintf(&b, "[%s] ", m)
	}
	b.WriteString(e.Message)
	keys := make([]string, 0, len(e.Data))
	for k := range e.Data {
		if k == "_module" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, " %s=%v", k, e.Data[k])
	}
	return b.String()
}
