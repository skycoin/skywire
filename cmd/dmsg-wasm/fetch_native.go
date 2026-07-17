//go:build js && wasm && !tinygo

// Package main cmd/dmsg-wasm/fetch_native.go c1-net-dmsg
package main

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"syscall/js"

	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
)

// dmsgHTTP is the HTTP-over-dmsg client backing jsFetch, built after connect.
var dmsgHTTP *http.Client

// onConnected wires the jsFetch transport once the dmsg client is up. Native
// build: a net/http client over a dmsghttp transport.
func onConnected(c *dmsg.Client) {
	dmsgHTTP = &http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, c)}
}

// jsFetch(pkHostHex, method, path, bodyOrNull, headersOrNull) -> Promise<{status, body, headers}>.
//
// Performs an HTTP request over dmsg to dmsg://<pkHost><path>, where pkHost is a
// public key (defaulting to the dmsg-HTTP port :80) or an explicit "pk:port".
// This is the transport the browser hypervisor UI uses to talk to a remote
// visor/hypervisor BY PUBLIC KEY — no clearnet, no exposed HTTP port. The
// request rides the client's existing dmsg session(s).
func jsFetch(_ js.Value, args []js.Value) interface{} {
	pkHost := args[0].String()
	method := args[1].String()
	path := args[2].String()
	var body string
	if len(args) > 3 && !args[3].IsNull() && !args[3].IsUndefined() {
		body = args[3].String()
	}
	// Optional request headers object (args[4]) — e.g. Cookie / X-CSRF-Token, so
	// the proxied request carries the page's auth.
	var reqHeaders js.Value
	hasHeaders := false
	if len(args) > 4 && !args[4].IsNull() && !args[4].IsUndefined() {
		reqHeaders = args[4]
		hasHeaders = true
	}
	return promise(func() (interface{}, error) {
		if dmsgHTTP == nil {
			return nil, errors.New("not connected; call connect() first")
		}
		host := pkHost
		if !strings.Contains(host, ":") {
			host += ":80" // default dmsg-HTTP port
		}
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		var bodyR io.Reader
		if body != "" {
			bodyR = strings.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, "dmsg://"+host+path, bodyR)
		if err != nil {
			return nil, err
		}
		if hasHeaders {
			keys := js.Global().Get("Object").Call("keys", reqHeaders)
			for i := 0; i < keys.Length(); i++ {
				k := keys.Index(i).String()
				req.Header.Set(k, reqHeaders.Get(k).String())
			}
		}
		resp, err := dmsgHTTP.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close() //nolint:errcheck
		b, _ := io.ReadAll(resp.Body)

		// Binary-safe body (Uint8Array) + headers, so the Service Worker can
		// build a correct Response for any content type (JS/CSS/HTML/fonts/etc).
		res := js.Global().Get("Object").New()
		res.Set("status", resp.StatusCode)
		buf := js.Global().Get("Uint8Array").New(len(b))
		js.CopyBytesToJS(buf, b)
		res.Set("body", buf)
		headers := js.Global().Get("Object").New()
		for k := range resp.Header {
			headers.Set(k, resp.Header.Get(k))
		}
		res.Set("headers", headers)
		return res, nil
	})
}
