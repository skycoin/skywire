// Package visor pkg/visor/api_browse_stream.go c3-vis-browse
package visor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// A browse frame's requests stream through here. /api/browse/clearnet reads a
// whole response (capped at browseMaxBody, 16 MiB), base64s it into JSON and
// hands it on, so a large download was cut short silently and every body sat
// in memory several times over. This answers with the proxied response itself:
// its status line and headers, then the body as it arrives, until the upstream
// ends — the desk reads it off the connection into a ReadableStream the frame's
// service worker responds with.
//
// Outside the /api group: its 30 s timeout would cut a long download.

// serveBrowseStream takes a BrowseClearnetRequest as a JSON body (Proxy
// defaults to this visor's resolving proxy) and answers with the upstream
// response, streamed.
func (v *Visor) serveBrowseStream(w http.ResponseWriter, r *http.Request) {
	var req BrowseClearnetRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, browseMaxBody)).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Proxy) == "" {
		p, ok := v.defaultBrowseProxy()
		if !ok {
			http.Error(w, "no proxy: name one, or enable dmsg_web (this visor's resolving proxy)", http.StatusBadRequest)
			return
		}
		req.Proxy = p
	}
	// Not r.Context(): the stream outlives the handler once hijacked. The
	// timer bounds the wait for the upstream's headers, not the body.
	ctx, cancel := context.WithCancel(context.Background())
	headers := time.AfterFunc(browseFetchTimeout, cancel)
	resp, err := v.proxyClearnetDo(ctx, req, 0)
	headers.Stop()
	if err != nil {
		cancel()
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		cancel()
		_ = resp.Body.Close() //nolint:errcheck
		http.Error(w, "connection cannot be taken over", http.StatusInternalServerError)
		return
	}
	desk, _, err := hj.Hijack()
	if err != nil {
		cancel()
		_ = resp.Body.Close() //nolint:errcheck
		return
	}
	go func() {
		defer cancel()
		defer resp.Body.Close()           //nolint:errcheck
		defer desk.Close()                //nolint:errcheck
		_ = desk.SetDeadline(time.Time{}) //nolint:errcheck
		if _, err := io.WriteString(desk, browseStreamHead(resp)); err != nil {
			return
		}
		_, _ = io.Copy(desk, resp.Body) //nolint:errcheck // a closed desk or upstream ends the stream either way
	}()
}

// browseStreamHead renders resp's status line and headers, every value of a
// repeated header kept (Set-Cookie), the connection-level ones dropped, and
// the address the request finally landed on after redirects.
func browseStreamHead(resp *http.Response) string {
	var b strings.Builder
	fmt.Fprintf(&b, "HTTP/1.1 %d %s\r\n", resp.StatusCode, http.StatusText(resp.StatusCode))
	for k, vs := range resp.Header {
		switch strings.ToLower(k) {
		case "connection", "keep-alive", "transfer-encoding", "proxy-connection", "upgrade", "trailer":
			continue
		}
		for _, val := range vs {
			fmt.Fprintf(&b, "%s: %s\r\n", k, strings.NewReplacer("\r", "", "\n", "").Replace(val))
		}
	}
	// Set-Cookie again as one JSON header: a page reading this with fetch()
	// never sees Set-Cookie, and the frame mirrors these into document.cookie.
	if sc := resp.Header.Values("Set-Cookie"); len(sc) > 0 {
		if j, err := json.Marshal(sc); err == nil {
			fmt.Fprintf(&b, "X-Realorigin-Set-Cookie: %s\r\n", j)
		}
	}
	if resp.Request != nil && resp.Request.URL != nil {
		fmt.Fprintf(&b, "X-Browse-Final-Url: %s\r\n", resp.Request.URL.String())
	}
	b.WriteString("Connection: close\r\n\r\n")
	return b.String()
}
