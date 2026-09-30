// Package visor pkg/visor/api_browse_client.go c3-vis-browse
package visor

import (
	"net/http"
	"net/http/cookiejar"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"
)

// The browse fetches a proxied page makes share one transport per proxy and
// one cookie jar, as a browser shares its connections and cookies across
// requests.
//
// A transport per request (kept-alives off) paid a new proxy stream, TCP
// connect and TLS handshake for every subresource: 6–19 s per request through
// the mesh (2026-09-30). Kept-alive connections are reused now, and bounded
// so they cannot pile up on the proxy: an earlier per-request transport with
// keep-alives on left each connection open, holding its proxy stream, until
// skysocks-client stopped answering.
//
// The jar is what makes a login stick: a server's Set-Cookie (HttpOnly
// session cookies included) is stored here and sent back on later requests
// to that site. The page's own frame cannot keep them — a browser ignores
// Set-Cookie on a response a service worker built.
const (
	browseMaxIdleConns        = 16
	browseMaxIdleConnsPerHost = 4
	browseIdleConnTimeout     = 30 * time.Second
)

var browseShared struct {
	mu  sync.Mutex
	tr  map[string]*http.Transport
	jar http.CookieJar
}

// browseTransport returns the shared transport for the proxy named key,
// building it with mk on first use. A failed build is not kept.
func browseTransport(key string, mk func() (*http.Transport, error)) (*http.Transport, error) {
	browseShared.mu.Lock()
	defer browseShared.mu.Unlock()
	if tr, ok := browseShared.tr[key]; ok {
		return tr, nil
	}
	if browseShared.tr == nil {
		browseShared.tr = make(map[string]*http.Transport)
	}
	tr, err := mk()
	if err != nil {
		return nil, err
	}
	tr.MaxIdleConns = browseMaxIdleConns
	tr.MaxIdleConnsPerHost = browseMaxIdleConnsPerHost
	tr.IdleConnTimeout = browseIdleConnTimeout
	tr.ForceAttemptHTTP2 = true
	browseShared.tr[key] = tr
	return tr, nil
}

// browseJar returns the cookie jar the browse fetches share.
func browseJar() http.CookieJar {
	browseShared.mu.Lock()
	defer browseShared.mu.Unlock()
	if browseShared.jar == nil {
		jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
		if err != nil {
			return nil
		}
		browseShared.jar = jar
	}
	return browseShared.jar
}

// browsePageCookieHeader carries the cookies the page itself holds (set
// through document.cookie, or mirrored there from an earlier response): the
// frame's service worker reads them from its cookie store, since a browser
// never shows a service worker the Cookie header.
const browsePageCookieHeader = "X-Realorigin-Cookie"

// applyPageCookies turns the page's cookies into the request's Cookie header,
// leaving out any the jar will add itself, so a cookie is never sent twice.
func applyPageCookies(req *http.Request, jar http.CookieJar) {
	raw := req.Header.Get(browsePageCookieHeader)
	req.Header.Del(browsePageCookieHeader)
	if raw == "" {
		return
	}
	have := map[string]bool{}
	if jar != nil {
		for _, c := range jar.Cookies(req.URL) {
			have[c.Name] = true
		}
	}
	page, err := http.ParseCookie(raw)
	if err != nil {
		return
	}
	for _, c := range page {
		if !have[c.Name] {
			req.AddCookie(c)
		}
	}
}
