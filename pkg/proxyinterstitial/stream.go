// Package proxyinterstitial pkg/proxyinterstitial/stream.go c4-app-web
//
// Streaming (HTTP chunked-transfer) variant of the route interstitial. Instead
// of the one-shot Content-Length page + client-side meta-refresh (interstitial.go),
// this holds the browser connection OPEN and flushes a progressive-HTML chunk per
// REAL route-setup attempt as it happens, then — once the route is up — flushes a
// final chunk that reloads the page into the now-live content.
//
// What "real" means here, precisely. The interstitial is minted AFTER a dial has
// already failed transiently; there is no in-flight route-setup to subscribe to
// at that point, and pkg/router exposes no per-hop / noise-handshake progress
// hook (DialOptions has no observer; even the mux-telemetry harness only
// synthesizes coarse "established/failed" events around its own DialRoutes call).
// So the streamer DRIVES a fresh attempt via a caller-supplied Probe and streams
// the real outcome of each attempt (StatusLine of the actual error, then success).
// That is the honest, coarse signal available today; the granular
// "2-hop via <pk> → noise handshake → route group up" lines the design sketches
// require a NEW router-side progress callback — see the package doc / the
// docs/proxy-status-and-interstitial.md "streaming seam" note. This file does not
// invent those events.
//
// Graceful fallback: an HTTP/1.0 client (no reliable chunked/progressive render)
// is served the existing one-shot page instead.
package proxyinterstitial

import (
	"bufio"
	"context"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Probe attempts to establish/verify the route to the interstitial's target.
// It returns nil when the route is ready (the browser should reload into live
// content), or an error while still warming. The streamer calls it on a loop
// (StreamConfig.Interval) until it succeeds, a non-transient error occurs, or
// StreamConfig.Deadline elapses. A successful Probe should leave the route warm
// (e.g. in the resolver's route pool) so the browser's reload hits real content.
type Probe func(ctx context.Context) error

// StreamConfig configures a streaming interstitial.
type StreamConfig struct {
	Target    string // host shown on the page (HTML-escaped by the renderer)
	Mechanism string // "skysocks" / "dmsg" / "skynet" — brands the copy
	Probe     Probe  // drives + observes the real route-setup attempt
	Interval  time.Duration
	Deadline  time.Duration
}

func (c StreamConfig) withDefaults() StreamConfig {
	if c.Interval <= 0 {
		c.Interval = 1 * time.Second
	}
	if c.Deadline <= 0 {
		c.Deadline = 30 * time.Second
	}
	return c
}

// StreamConn returns a net.Conn that serves a chunked, progressively-rendered
// interstitial driven by cfg.Probe, suitable for handing to go-socks5 as the
// tunnel conn (same injection model as Conn). Writes (the browser's request) are
// read to completion first; an HTTP/1.0 request falls back to the one-shot page.
func StreamConn(ctx context.Context, cfg StreamConfig) net.Conn {
	cfg = cfg.withDefaults()
	srvConn, cliConn := net.Pipe()
	go func() {
		defer srvConn.Close() //nolint:errcheck
		br := bufio.NewReader(srvConn)
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		// HTTP/1.0 clients don't reliably render a streamed/chunked body — serve
		// the one-shot page (still branded + auto-refreshing) instead.
		if req.ProtoMajor == 1 && req.ProtoMinor == 0 {
			_, _ = srvConn.Write(httpResponse(cfg.Target, "", cfg.Mechanism, false)) //nolint:errcheck
			return
		}
		head := "HTTP/1.1 200 OK\r\n" +
			"Content-Type: text/html; charset=utf-8\r\n" +
			"Transfer-Encoding: chunked\r\n" +
			"Cache-Control: no-store, must-revalidate\r\n" +
			"Connection: close\r\n\r\n"
		if _, err := io.WriteString(srvConn, head); err != nil {
			return
		}
		cw := &chunkWriter{w: srvConn}
		DriveStream(ctx, cw, cfg)
		_ = cw.End() //nolint:errcheck
	}()
	return cliConn
}

// DriveStream renders the streaming interstitial to w: the opening shell, one
// progress line per real Probe attempt, and a closing chunk (reload-into-content
// on success, or an error + manual-retry on a hard failure / deadline). Exported
// so a caller that already streams over an http.ResponseWriter / io.Pipe (e.g.
// the browse-origin reverse proxy) can reuse the exact same rendering without the
// chunked-conn framing. w is written as raw HTML fragments; the HTTP layer
// (chunkWriter for the SOCKS path, the net/http server for the proxy path) frames
// and flushes them.
func DriveStream(ctx context.Context, w io.Writer, cfg StreamConfig) {
	cfg = cfg.withDefaults()
	emit := func(s string) bool { _, err := io.WriteString(w, s); return err == nil }
	if f, ok := w.(flusher); ok {
		orig := emit
		emit = func(s string) bool {
			if !orig(s) {
				return false
			}
			f.Flush()
			return true
		}
	}

	if !emit(StreamOpen(cfg.Target, cfg.Mechanism)) {
		return
	}
	emit(StreamStep("Building a route over "+mechanismLabel(cfg.Mechanism)+"…", "active"))

	if cfg.Probe == nil {
		emit(StreamClose(false, "no route-setup probe available"))
		return
	}

	deadline := time.Now().Add(cfg.Deadline)
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		err := cfg.Probe(ctx)
		if err == nil {
			emit(StreamStep("Route group up", "done"))
			emit(StreamClose(true, ""))
			return
		}
		if !IsTransient(err) {
			emit(StreamStep(StatusLine(err), "err"))
			emit(StreamClose(false, err.Error()))
			return
		}
		if time.Now().After(deadline) {
			emit(StreamStep(StatusLine(err), "err"))
			emit(StreamClose(false, "route still warming — try again"))
			return
		}
		emit(StreamStep(StatusLine(err), "active"))
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// flusher is net/http's http.Flusher without importing it here (the SOCKS path's
// chunkWriter also implements it), so DriveStream flushes each fragment on either
// transport.
type flusher interface{ Flush() }

// StreamOpen is the opening HTML of a streaming interstitial: the branded card
// header and the (open) live progress list. Subsequent StreamStep fragments
// append <li> rows to it; StreamClose finishes the list and the document.
func StreamOpen(target, _ string) string {
	title := "Connecting over skywire…"
	heading := "Building a route over the mesh…"
	msg := "Establishing a private route to this site. Live progress below."
	return `<!doctype html><html lang="en"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width, initial-scale=1">` +
		`<title>` + title + `</title><style>` + css + `</style></head>` +
		`<body><div id="mesh-boot"><div class="card" id="mesh-card">` +
		brandMark +
		`<div class="sp" id="mesh-sp"></div>` +
		`<h1 id="mesh-title">` + html.EscapeString(heading) + `</h1>` +
		`<p id="mesh-msg">` + html.EscapeString(msg) + `</p>` +
		`<small id="mesh-host">` + html.EscapeString(strings.TrimSpace(target)) + `</small>` +
		`<ul class="steps" id="mesh-steps">` +
		// Enough leading padding so browsers begin incremental render immediately
		// rather than buffering a minimum before first paint.
		"<!-- " + strings.Repeat("skywire ", 128) + "-->"
}

// StreamStep is one live progress line. state is "active" (in progress),
// "done" (completed) or "err" (failed). text is HTML-escaped.
func StreamStep(text, state string) string {
	cls := "active"
	switch state {
	case "done":
		cls = "done"
	case "err":
		cls = "err"
	}
	return `<li class="` + cls + `">` + html.EscapeString(text) + `</li>`
}

// StreamClose finishes the document. On ok it appends a "route up" line and a
// script that reloads the SAME URL — which now streams real content because the
// successful probe left the route warm. On failure it shows detail + a manual
// retry, and does NOT auto-reload.
func StreamClose(ok bool, detail string) string {
	var b strings.Builder
	b.WriteString(`</ul>`)
	if ok {
		b.WriteString(`<p class="ready">Route up — loading the page…</p>`)
		// replace() so the interstitial isn't left in history; the reload
		// re-requests through the proxy and hits the now-warm route.
		b.WriteString(`<script>location.replace(location.href)</script>`)
	} else {
		if d := strings.TrimSpace(detail); d != "" {
			b.WriteString(`<p id="mesh-detail" style="display:block">` + html.EscapeString(d) + `</p>`)
		}
		b.WriteString(`<button class="btn" onclick="location.reload()">Retry</button>`)
	}
	b.WriteString(`</div></div></body></html>`)
	return b.String()
}

// chunkWriter frames each Write as a single HTTP/1.1 chunk and flushes it (a
// net.Pipe Write already delivers synchronously, so Flush is a no-op but lets
// chunkWriter satisfy the flusher interface DriveStream keys on).
type chunkWriter struct{ w io.Writer }

func (c *chunkWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if _, err := fmt.Fprintf(c.w, "%x\r\n", len(p)); err != nil {
		return 0, err
	}
	if _, err := c.w.Write(p); err != nil {
		return 0, err
	}
	if _, err := io.WriteString(c.w, "\r\n"); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *chunkWriter) Flush() {}

// End writes the terminating zero-length chunk.
func (c *chunkWriter) End() error {
	_, err := io.WriteString(c.w, "0\r\n\r\n")
	return err
}
