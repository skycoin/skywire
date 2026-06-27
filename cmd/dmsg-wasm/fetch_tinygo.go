//go:build js && wasm && tinygo

package main

import (
	"errors"
	"syscall/js"

	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
)

// onConnected is a no-op under TinyGo: jsFetch dials the dmsg client directly
// per request via dmsgclient.FetchOverDmsg (no persistent net/http client).
func onConnected(_ *dmsg.Client) {}

// jsFetch(pkHostHex, method, path, bodyOrNull, headersOrNull) -> Promise<{status, body, headers}>.
//
// TinyGo build: identical surface to the native jsFetch, but the HTTP-over-dmsg
// round-trip is performed net/http-free by dmsgclient.FetchOverDmsg (net/http is
// broken on the TinyGo js target). Talks to a REMOTE visor/hypervisor BY PUBLIC
// KEY over the client's existing dmsg session(s).
func jsFetch(_ js.Value, args []js.Value) interface{} {
	pkHost := args[0].String()
	method := args[1].String()
	path := args[2].String()
	var body []byte
	if len(args) > 3 && !args[3].IsNull() && !args[3].IsUndefined() {
		body = []byte(args[3].String())
	}
	reqHeaders := map[string]string{}
	if len(args) > 4 && !args[4].IsNull() && !args[4].IsUndefined() {
		hdr := args[4]
		keys := js.Global().Get("Object").Call("keys", hdr)
		for i := 0; i < keys.Length(); i++ {
			k := keys.Index(i).String()
			reqHeaders[k] = hdr.Get(k).String()
		}
	}
	return promise(func() (interface{}, error) {
		if client == nil {
			return nil, errors.New("not connected; call connect() first")
		}
		status, respHeaders, b, err := dmsgclient.FetchOverDmsg(ctx, client, method, pkHost, path, reqHeaders, body)
		if err != nil {
			return nil, err
		}
		// Binary-safe body (Uint8Array) + headers, so the Service Worker can build
		// a correct Response for any content type (JS/CSS/HTML/fonts/etc).
		res := js.Global().Get("Object").New()
		res.Set("status", status)
		buf := js.Global().Get("Uint8Array").New(len(b))
		js.CopyBytesToJS(buf, b)
		res.Set("body", buf)
		headers := js.Global().Get("Object").New()
		for k, v := range respHeaders {
			headers.Set(k, v)
		}
		res.Set("headers", headers)
		return res, nil
	})
}
