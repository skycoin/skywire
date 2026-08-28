//go:build js && wasm

// Package main cmd/wasm-visor/browsesw_js.go c3-vis-wasm
// The transport service worker for a real-origin browse frame, in Go.
//
// This is an ALTERNATIVE to the JavaScript worker that github.com/0magnet/realorigin
// ships and that every deployment uses by default. It exists to be tested, not to
// be deployed: `hv serve --browse-origin-wasm` opts into it explicitly.
//
// Understand what it costs before switching it on. The JS worker's security
// property is that you can read it — a hundred lines that name no transport, on
// the untrusted origin B. This one is a slice of the visor binary, so the same
// artifact that holds the visor is served to B, and "B cannot reach a key"
// stops being provable by inspection and becomes a claim about a role flag.
// wasmRole() fails closed in a service worker for that reason (see shell_js.go),
// but a flag defaulting safely is still weaker than code that is not there.
//
// The worker does not register its own listeners. A service worker must add its
// functional event listeners during the initial synchronous evaluation of its
// script, and this module is instantiated asynchronously — a listener added
// after that misses the first fetch of every cold start, which shows up as a
// 404 fallthrough rather than an error. So browse-sw-loader.js registers them
// and calls what this installs.
package main

import (
	"syscall/js"
)

// installBrowseSW publishes __realOriginSWFetch(request, clientId) -> Promise<Response>,
// which the loader calls from inside its own synchronously registered handler.
func installBrowseSW() {
	js.Global().Set("__realOriginSWFetch", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) < 1 {
			return rejectedPromise("realorigin: no request")
		}
		req := args[0]
		clientID := ""
		if len(args) > 1 && args[1].Type() == js.TypeString {
			clientID = args[1].String()
		}
		return bridgeFetchPromise(req, clientID)
	}))
}

// bridgeFetchPromise mirrors realorigin's sw.js: find a controlling page, hand it
// the request over a per-request MessagePort, and turn the reply into a Response.
// A channel per request is what keeps concurrent subresource loads from colliding.
func bridgeFetchPromise(req js.Value, clientID string) js.Value {
	g := js.Global()
	return g.Get("Promise").New(js.FuncOf(func(_ js.Value, pargs []js.Value) any {
		resolve := pargs[0]
		go func() {
			defer func() {
				// A panic here would otherwise kill the worker and take every
				// pending request with it; a 502 keeps the page diagnosable.
				if r := recover(); r != nil {
					resolve.Invoke(newResponse(g, js.Null(), 502,
						"real-origin: worker panic"))
				}
			}()
			client := findClient(g, clientID)
			if !client.Truthy() {
				resolve.Invoke(newResponse(g, js.Null(), 503,
					"real-origin: no controlling page to relay through"))
				return
			}
			resolve.Invoke(relay(g, client, req))
		}()
		return nil
	}))
}

// findClient prefers the client the fetch came from and falls back to any window,
// which is what lets a frame still load when clientId is empty (a cold worker
// answering a request whose client it never saw).
func findClient(g js.Value, clientID string) js.Value {
	clients := g.Get("clients")
	if clientID != "" {
		if c, ok := await(clients.Call("get", clientID)); ok && c.Truthy() {
			return c
		}
	}
	all, ok := await(clients.Call("matchAll", map[string]any{
		"type": "window", "includeUncontrolled": true,
	}))
	if !ok || !all.Truthy() || all.Length() == 0 {
		return js.Null()
	}
	return all.Index(0)
}

// relay posts the request to client and blocks until the reply arrives.
func relay(g js.Value, client, req js.Value) js.Value {
	method := req.Get("method").String()

	var bodyBuf js.Value = js.Null()
	if method != "GET" && method != "HEAD" {
		if b, ok := await(req.Call("clone").Call("arrayBuffer")); ok {
			bodyBuf = b
		}
	}

	headers := map[string]any{}
	req.Get("headers").Call("forEach", js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) >= 2 {
			headers[a[1].String()] = a[0].String()
		}
		return nil
	}))

	mc := g.Get("MessageChannel").New()
	done := make(chan js.Value, 1)
	mc.Get("port1").Set("onmessage", js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) > 0 {
			done <- a[0].Get("data")
		} else {
			done <- js.Null()
		}
		return nil
	}))

	transfer := []any{mc.Get("port2")}
	if bodyBuf.Truthy() {
		transfer = append(transfer, bodyBuf)
	}
	client.Call("postMessage", map[string]any{
		"type": "realorigin-fetch",
		"req": map[string]any{
			"url": req.Get("url").String(), "method": method,
			"headers": headers, "body": bodyBuf,
		},
	}, transfer)

	// The 60s ceiling matches the JS worker: a mesh route can be slow to build,
	// but a request that never returns must not hold the frame open forever.
	timer := g.Call("setTimeout", js.FuncOf(func(_ js.Value, _ []js.Value) any {
		select {
		case done <- js.Undefined():
		default:
		}
		return nil
	}), 60000)

	data := <-done
	g.Call("clearTimeout", timer)

	if data.Type() == js.TypeUndefined {
		return newResponse(g, js.Null(), 504, "real-origin: upstream timeout")
	}
	if !data.Truthy() {
		return newResponse(g, js.Null(), 502, "real-origin: empty reply")
	}
	if e := data.Get("error"); e.Truthy() {
		return newResponse(g, js.Null(), 502, "real-origin: "+e.String())
	}

	status := 200
	if s := data.Get("status"); s.Type() == js.TypeNumber && s.Int() > 0 {
		status = s.Int()
	}
	h := g.Get("Headers").New()
	if hv := data.Get("headers"); hv.Truthy() {
		keys := g.Get("Object").Call("keys", hv)
		for i := 0; i < keys.Length(); i++ {
			k := keys.Index(i).String()
			// Drop hop-by-hop and framing headers: the browser recomputes its
			// own, and a stale content-length truncates the body.
			switch lower(k) {
			case "content-length", "transfer-encoding", "connection":
				continue
			}
			safeSet(h, k, hv.Get(k))
		}
	}
	body := data.Get("body")
	if !body.Truthy() {
		body = js.Null()
	}
	return g.Get("Response").New(body, map[string]any{"status": status, "headers": h})
}

// safeSet ignores a header the platform rejects, exactly as the JS worker's
// try/catch does: one malformed name from upstream must not fail the response.
func safeSet(h js.Value, k string, v js.Value) {
	defer func() { _ = recover() }()
	if v.Type() == js.TypeString {
		h.Call("set", k, v.String())
	}
}

func newResponse(g js.Value, body js.Value, status int, text string) js.Value {
	if text != "" {
		return g.Get("Response").New(text, map[string]any{"status": status})
	}
	return g.Get("Response").New(body, map[string]any{"status": status})
}

func rejectedPromise(msg string) js.Value {
	g := js.Global()
	return g.Get("Promise").Call("resolve",
		g.Get("Response").New(msg, map[string]any{"status": 500}))
}

// await blocks the calling goroutine on a JS promise. Safe only off the event
// loop's own turn, which is why every caller runs inside the goroutine started
// by bridgeFetchPromise.
func await(p js.Value) (js.Value, bool) {
	if !p.Truthy() || p.Get("then").Type() != js.TypeFunction {
		return p, p.Truthy()
	}
	type res struct {
		v  js.Value
		ok bool
	}
	ch := make(chan res, 1)
	p.Call("then",
		js.FuncOf(func(_ js.Value, a []js.Value) any {
			if len(a) > 0 {
				ch <- res{a[0], true}
			} else {
				ch <- res{js.Undefined(), true}
			}
			return nil
		}),
		js.FuncOf(func(_ js.Value, _ []js.Value) any { ch <- res{js.Undefined(), false}; return nil }),
	)
	r := <-ch
	return r.v, r.ok
}

func lower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}
