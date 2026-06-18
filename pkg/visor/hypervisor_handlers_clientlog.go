// Package visor pkg/visor/hypervisor_handlers_clientlog.go — sink for
// browser-side error reports from the hypervisor UI.
//
// The hvui installs a global error reporter (a window.onerror /
// unhandledrejection hook + an Angular ErrorHandler + a console.error/
// warn wrapper) that POSTs each client-side error here. The handler
// writes them to the visor log under the "hvui_client" logger, so an
// operator debugging a broken UI page (a dead tab, a failed XHR, a
// thrown change-detection error) can read the browser's errors right
// in `./local/log/skywire.log` instead of copy-pasting the dev-tools
// console.
//
// It's intentionally unauthenticated — a broken page (or one that
// crashes before login) must still be able to report — so it is
// hardened against abuse of an open log sink: the request body is
// size-capped, reports are rate-limited, and every field is sanitized
// (control chars stripped) to prevent log-injection of forged lines.
package visor

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/logging"
)

// clientLogEntry is the JSON shape the hvui error reporter POSTs.
type clientLogEntry struct {
	Level   string `json:"level"`   // "error" | "warn" | "log"
	Message string `json:"message"` // the error message / console args
	Stack   string `json:"stack"`   // JS stack trace, when available
	URL     string `json:"url"`     // page URL the error occurred on
	Line    int    `json:"line"`    // source line, when available
}

const (
	// clientLogMaxBody caps the POST body — a stack trace plus message
	// fits comfortably; anything larger is almost certainly abuse.
	clientLogMaxBody = 8 << 10 // 8 KiB
	// clientLogMaxField truncates an individual field in the log line.
	clientLogMaxField = 2 << 10 // 2 KiB
	// clientLogRatePerWindow / clientLogWindow bound how many reports
	// are logged per window across all callers — a dead page can loop
	// the same error rapidly, and we don't want it to drown the log.
	clientLogRatePerWindow = 20
	clientLogWindow        = 10 * time.Second
)

// postClientLog returns the handler for POST /api/client-log. The
// rate-limit state is captured in the closure so it's per-handler
// (i.e. per hypervisor), not global.
func (hv *Hypervisor) postClientLog() http.HandlerFunc {
	// Derive the logger nil-safely: this runs at route-registration time, and
	// hv.visor is nil in some hypervisor-only tests (TestNewNode). Mirror
	// NewHypervisor's own pattern instead of dereferencing hv.visor eagerly.
	mLogger := logging.NewMasterLogger()
	if hv.visor != nil {
		mLogger = hv.visor.MasterLogger()
	}
	log := mLogger.PackageLogger("hvui_client")
	var (
		mu          sync.Mutex
		windowStart time.Time
		windowCount int
	)
	allow := func(now time.Time) bool {
		mu.Lock()
		defer mu.Unlock()
		if now.Sub(windowStart) > clientLogWindow {
			windowStart = now
			windowCount = 0
		}
		if windowCount >= clientLogRatePerWindow {
			return false
		}
		windowCount++
		return true
	}

	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, clientLogMaxBody)
		var e clientLogEntry
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "bad client-log body"})
			return
		}
		// Always 204 quickly — the reporter is fire-and-forget and must
		// never get a response that itself triggers another error.
		w.WriteHeader(http.StatusNoContent)

		if !allow(time.Now()) {
			return // silently drop over-rate reports
		}

		entry := log.
			WithField("url", sanitizeClientLog(e.URL, clientLogMaxField)).
			WithField("line", e.Line)
		if s := sanitizeClientLog(e.Stack, clientLogMaxField); s != "" {
			entry = entry.WithField("stack", s)
		}
		msg := sanitizeClientLog(e.Message, clientLogMaxField)
		if msg == "" {
			msg = "(empty client message)"
		}
		// Client errors log at Warn (not Error) so they're visible in
		// the visor log without masquerading as visor-internal errors;
		// the "level" field preserves the browser-side severity.
		entry.WithField("level", sanitizeClientLog(e.Level, 16)).Warn("hvui client: " + msg)
	}
}

// sanitizeClientLog strips control characters (notably CR/LF, which
// would let a caller inject forged log lines) and truncates to max.
func sanitizeClientLog(s string, max int) string {
	if len(s) > max {
		s = s[:max] + "…(truncated)"
	}
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}
