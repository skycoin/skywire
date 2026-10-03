// Package visor pkg/visor/wasmserve_bootreport.go c3-vis-core
package visor

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/logging"
)

// A browser whose desk fails to start says so here: the page posts what it
// saw (bootReportJS) and the server logs it. A desk that never boots, on a
// phone nobody here can attach a debugger to, otherwise leaves no trace at all.
const (
	bootReportMaxBytes  = 8 << 10
	bootReportPerWindow = 10 // per client address
	bootReportAllWindow = 200
	bootReportWindow    = time.Minute
	bootReportFieldMax  = 600
	bootReportStackMax  = 2000
)

// bootReportLimiter bounds how many reports are logged per client address and
// overall within a window, so the endpoint cannot be used to flood the log.
type bootReportLimiter struct {
	mu     sync.Mutex
	start  time.Time
	all    int
	perKey map[string]int
}

func (l *bootReportLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.start) >= bootReportWindow {
		l.start, l.all, l.perKey = now, 0, make(map[string]int)
	}
	if l.all >= bootReportAllWindow || l.perKey[key] >= bootReportPerWindow {
		return false
	}
	l.all++
	l.perKey[key]++
	return true
}

// bootReportClient is the address a report is counted against: the first
// X-Forwarded-For hop when a reverse proxy sits in front, else the peer. It is
// used only for the rate limit and never logged.
func bootReportClient(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return strings.TrimSpace(strings.Split(fwd, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// bootReportHandler logs a boot report posted by the desk page.
func bootReportHandler(log *logging.Logger) http.HandlerFunc {
	lim := &bootReportLimiter{perKey: make(map[string]int)}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		if !lim.allow(bootReportClient(r), time.Now()) {
			http.Error(w, "too many reports", http.StatusTooManyRequests)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, bootReportMaxBytes))
		if err != nil {
			http.Error(w, "report too large", http.StatusRequestEntityTooLarge)
			return
		}
		var rep map[string]any
		if err := json.Unmarshal(body, &rep); err != nil {
			http.Error(w, "not a JSON object", http.StatusBadRequest)
			return
		}
		entry := log.WithField("kind", bootReportField(rep, "kind", 40))
		for _, k := range []string{"stage", "version", "ua", "error", "src", "url"} {
			if v := bootReportField(rep, k, bootReportFieldMax); v != "" {
				entry = entry.WithField(k, v)
			}
		}
		for _, k := range []string{"ms", "memory_gb", "cores", "cross_origin_isolated", "shared_array_buffer", "webassembly", "standalone", "service_worker"} {
			if v, ok := rep[k]; ok {
				entry = entry.WithField(k, v)
			}
		}
		if v := bootReportField(rep, "stack", bootReportStackMax); v != "" {
			entry = entry.WithField("stack", v)
		}
		if v := bootReportField(rep, "console", bootReportStackMax); v != "" {
			entry = entry.WithField("console", v)
		}
		entry.Warn("a browser reported a desk that did not start")
		w.WriteHeader(http.StatusNoContent)
	}
}

// bootReportField returns rep[k] as a string of at most max bytes.
func bootReportField(rep map[string]any, k string, maxLen int) string {
	v, ok := rep[k]
	if !ok || v == nil {
		return ""
	}
	var s string
	switch t := v.(type) {
	case string:
		s = t
	case []any:
		parts := make([]string, 0, len(t))
		for _, p := range t {
			parts = append(parts, fmt.Sprint(p))
		}
		s = strings.Join(parts, " | ")
	default:
		s = fmt.Sprint(t)
	}
	if len(s) > maxLen {
		s = s[:maxLen] + "…"
	}
	return s
}

// bootReportJS runs first on the desk page. Until the boot overlay is
// dismissed it reports script errors, unhandled rejections, resources that
// failed to load, a boot that rejected, and a boot still unfinished after two
// minutes, each with the overlay's status line, the last console errors and
// warnings (where a Go panic or a worker crash shows), and what the browser
// supports. At most five reports per page load; none once the desk is up.
const bootReportJS = `(function () {
  'use strict';
  var t0 = Date.now(), sent = 0, done = false, recent = [];
  function stage() {
    var el = document.getElementById('boot-msg');
    return el ? el.textContent : 'before the page parsed';
  }
  function report(kind, extra) {
    if (done || sent >= 5) return;
    sent++;
    var r = {
      kind: kind, stage: stage(), ms: Date.now() - t0,
      version: '__SKYWIRE_SERVED_VERSION__', url: location.pathname,
      visible: document.visibilityState,
      ua: navigator.userAgent, memory_gb: navigator.deviceMemory || 0,
      cores: navigator.hardwareConcurrency || 0,
      cross_origin_isolated: !!self.crossOriginIsolated,
      shared_array_buffer: typeof SharedArrayBuffer !== 'undefined',
      webassembly: typeof WebAssembly === 'object',
      service_worker: 'serviceWorker' in navigator,
      standalone: !!(window.matchMedia && matchMedia('(display-mode: standalone)').matches),
      console: recent.slice(-12)
    };
    for (var k in extra) if (Object.prototype.hasOwnProperty.call(extra, k)) r[k] = extra[k];
    var body = JSON.stringify(r);
    try {
      if (!(navigator.sendBeacon && navigator.sendBeacon('boot-report', new Blob([body], {type: 'application/json'})))) {
        fetch('boot-report', {method: 'POST', body: body, keepalive: true, headers: {'Content-Type': 'application/json'}}).catch(function () {});
      }
    } catch (e) {}
  }
  window.__skywireBootReport = report;
  ['error', 'warn'].forEach(function (lvl) {
    var orig = console[lvl];
    console[lvl] = function () {
      try {
        recent.push(lvl + ': ' + Array.prototype.map.call(arguments, function (a) {
          return a && a.stack ? String(a.stack) : String(a);
        }).join(' ').slice(0, 300));
        if (recent.length > 30) recent.shift();
      } catch (e) {}
      return orig.apply(console, arguments);
    };
  });
  addEventListener('error', function (e) {
    var t = e.target;
    if (t && t !== window && (t.src || t.href)) { report('load-error', {src: t.src || t.href}); return; }
    report('error', {
      error: String(e.message || e),
      src: e.filename ? e.filename + ':' + e.lineno + ':' + e.colno : '',
      stack: e.error && e.error.stack ? String(e.error.stack) : ''
    });
  }, true);
  addEventListener('unhandledrejection', function (e) {
    var x = e.reason;
    report('rejection', {error: String((x && x.message) || x), stack: x && x.stack ? String(x.stack) : ''});
  });
  document.addEventListener('DOMContentLoaded', function () {
    var boot = document.getElementById('boot');
    if (!boot) return;
    var check = function () { if (/\bgone\b/.test(boot.className)) done = true; };
    check();
    new MutationObserver(check).observe(boot, {attributes: true, attributeFilter: ['class']});
  });
  setTimeout(function () { report('stalled', {}); }, 120000);
})();`
