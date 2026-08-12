//go:build js && wasm

// Package main cmd/wasm-visor/shellmesh_js.go c3-vis-wasm
// The mesh as the shell's network: fetch and dial by public key, over dmsg,
// from the terminal in the visor tab.
//
// websh already has curl and nc, but both are the browser's: fetch() and
// WebSocket, which reach clearnet origins and nothing else. What makes a shell
// in a browser tab interesting here is that the visor beside it reaches a
// keyless, PK-addressed overlay — so these applets route through the visor
// instead, and a public key is a hostname:
//
//	dcurl dmsg://<pk>:80/                    # or the resolver's aliases:
//	dcurl dmsg://tpd/health
//	dcurl -s dmsg://rf/health | jq .
//	dial tpd                                 # reachable, and how fast
//	aliases                                  # what names resolve here
//
// # Which instance is talking
//
// These go through globalThis.skywireVisor rather than reaching for the
// visor's own dmsg client, because the shell usually is not the visor: the
// terminal needs a DOM and the visor normally runs in a SharedWorker, so the
// tab holds a SECOND wasm instance in the "shell" role whose package-level
// dmsgC and resolverAliases are nil and empty. Going through skywireVisor
// works either way — it is the real function table in the visor instance and a
// postMessage proxy in the tab — which is the same reason the visor applets in
// shell_js.go call hvApi rather than hvCore directly.
//
// # Capability
//
// These grant no reach the tab does not already have: the visor's in-tab
// browser resolves and fetches dmsg:// through this very method (fetchDmsg,
// with the alias handling in resolve_js.go), and the visor dials the same
// peers for its own transport and route lookups. This is that reach, in a
// shell.
//
// What WOULD be new is executing on a remote peer — dmsgpty exec over the
// mesh. That is deliberately absent; it belongs behind the existing exec gate
// (visorconfig.Pty.AllowRPCExec, off by default), in the milestone that adds
// it.
package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"syscall/js"
	"time"

	"github.com/skycoin/skywire/third_party/0magnet/sh/v3/interp"
	"github.com/skycoin/skywire/third_party/0magnet/websh/shell"
)

// visorCall invokes a visor method and awaits it, so a proxied promise and a
// direct return look the same to the caller.
func visorCall(method string, args ...interface{}) (js.Value, error) {
	v := js.Global().Get("skywireVisor")
	if !v.Truthy() {
		return js.Undefined(), fmt.Errorf("no visor in this tab")
	}
	fn := v.Get(method)
	if fn.Type() != js.TypeFunction {
		return js.Undefined(), fmt.Errorf("this visor does not expose %s", method)
	}
	return jsAwait(v.Call(method, args...))
}

// meshHost splits a dmsg target into the host the visor's resolver understands
// and the path. Resolution itself stays on the visor side (resolveFetchHost):
// all this does is give a bare alias the .dmsg suffix that resolver keys on,
// so "tpd" works as well as "tpd.dmsg" — a public key goes through verbatim.
func meshHost(raw string) (host, path string) {
	s := raw
	for _, scheme := range []string{"dmsg://", "skynet://", "http://"} {
		if rest, ok := strings.CutPrefix(s, scheme); ok {
			s = rest
			break
		}
	}
	path = "/"
	if i := strings.IndexByte(s, '/'); i >= 0 {
		path, s = s[i:], s[:i]
	}
	// The visor's resolver keys on the .dmsg suffix — "tpd.dmsg" resolves, a
	// bare "tpd" does not — so add it for anything that is not already a
	// public key or suffixed. A raw key is 66 hex characters and must go
	// through untouched.
	host = s
	if !strings.Contains(host, ".") && !isPubKey(strings.SplitN(host, ":", 2)[0]) {
		if i := strings.IndexByte(host, ':'); i >= 0 {
			host = host[:i] + ".dmsg" + host[i:]
		} else {
			host += ".dmsg"
		}
	}
	return host, path
}

// isPubKey reports whether s looks like a hex-encoded public key, which the
// resolver takes verbatim.
func isPubKey(s string) bool {
	if len(s) != 66 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// registerMeshApplets adds the mesh-backed network commands to the shell.
var registerMeshApplets = sync.OnceFunc(func() {
	shell.RegisterApplet("dcurl",
		"fetch over dmsg by public key: dcurl [-s] [-I] dmsg://<pk|alias>[:port][/path]",
		runDcurl)
	shell.RegisterApplet("dial",
		"time a fetch to a peer over dmsg: dial <pk|alias>[:port]",
		runDial)
	shell.RegisterApplet("aliases",
		"list the resolver's service aliases and the public keys they name",
		runAliases)
})

// fetchMesh performs one dmsg fetch through the visor.
func fetchMesh(method, host, path string) (status int, headers js.Value, body string, err error) {
	res, err := visorCall("fetchDmsg", host, method, path)
	if err != nil {
		return 0, js.Undefined(), "", err
	}
	if !res.Truthy() {
		return 0, js.Undefined(), "", fmt.Errorf("no response")
	}
	if s := res.Get("status"); s.Type() == js.TypeNumber {
		status = s.Int()
	}
	if b := res.Get("body"); b.Type() == js.TypeString {
		body = b.String()
	}
	return status, res.Get("headers"), body, nil
}

// runDcurl implements the dcurl applet.
func runDcurl(_ context.Context, _ *shell.Shell, hc *interp.HandlerContext, args []string) int {
	silent, headOnly := false, false
	var target string
	for _, a := range args {
		switch a {
		case "-s", "--silent":
			silent = true
		case "-I", "--head":
			headOnly = true
		default:
			if strings.HasPrefix(a, "-") {
				_, _ = fmt.Fprintf(hc.Stderr, "dcurl: unknown option %s\n", a) //nolint:errcheck
				return 2
			}
			target = a
		}
	}
	if target == "" {
		_, _ = fmt.Fprintln(hc.Stderr, "usage: dcurl [-s] [-I] dmsg://<pk|alias>[:port][/path]") //nolint:errcheck
		return 2
	}

	host, path := meshHost(target)
	method := "GET"
	if headOnly {
		method = "HEAD"
	}
	status, headers, body, err := fetchMesh(method, host, path)
	if err != nil {
		_, _ = fmt.Fprintf(hc.Stderr, "dcurl: %v\n", err) //nolint:errcheck
		return 1
	}
	if !silent {
		_, _ = fmt.Fprintf(hc.Stderr, "HTTP %d\n", status) //nolint:errcheck
	}
	switch {
	case headOnly:
		if headers.Truthy() {
			keys := js.Global().Get("Object").Call("keys", headers)
			for i := 0; i < keys.Length(); i++ {
				k := keys.Index(i).String()
				_, _ = fmt.Fprintf(hc.Stdout, "%s: %s\n", k, headers.Get(k).String()) //nolint:errcheck
			}
		}
	case body != "":
		_, _ = fmt.Fprintln(hc.Stdout, body) //nolint:errcheck
	}
	if status < 200 || status >= 300 {
		return 1
	}
	return 0
}

// runDial times a fetch to a peer: the mesh's ping. It reports reachability
// over dmsg rather than raw stream liveness, because reaching a peer through
// the visor is what the shell can actually do from the tab.
func runDial(_ context.Context, _ *shell.Shell, hc *interp.HandlerContext, args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(hc.Stderr, "usage: dial <pk|alias>[:port]") //nolint:errcheck
		return 2
	}
	host, path := meshHost(args[0])
	start := time.Now()
	status, _, _, err := fetchMesh("HEAD", host, path)
	elapsed := time.Since(start).Round(time.Millisecond)
	if err != nil {
		_, _ = fmt.Fprintf(hc.Stderr, "dial: %s unreachable after %s: %v\n", host, elapsed, err) //nolint:errcheck
		return 1
	}
	_, _ = fmt.Fprintf(hc.Stdout, "%s reachable in %s (HTTP %d)\n", host, elapsed, status) //nolint:errcheck
	return 0
}

// runAliases prints the resolver's alias table, so the service names dcurl and
// dial accept are discoverable without knowing the deployment's keys.
func runAliases(_ context.Context, _ *shell.Shell, hc *interp.HandlerContext, _ []string) int {
	none := func() int {
		_, _ = fmt.Fprintln(hc.Stderr, "aliases: none yet — the visor resolves them once it has its service set") //nolint:errcheck
		return 1
	}
	res, err := visorCall("meshAliases")
	if err != nil {
		_, _ = fmt.Fprintf(hc.Stderr, "aliases: %v\n", err) //nolint:errcheck
		return 1
	}
	if !res.Truthy() {
		return none()
	}
	keys := js.Global().Get("Object").Call("keys", res)
	names := make([]string, 0, keys.Length())
	for i := 0; i < keys.Length(); i++ {
		names = append(names, keys.Index(i).String())
	}
	if len(names) == 0 {
		return none()
	}
	sort.Strings(names)
	for _, name := range names {
		_, _ = fmt.Fprintf(hc.Stdout, "%-8s %s\n", name, res.Get(name).String()) //nolint:errcheck
	}
	return 0
}

// jsMeshAliases exposes the resolver's alias table to the tab, so a shell in
// another wasm instance can list what names resolve here. It runs in the visor
// instance, where resolverAliases is populated.
func jsMeshAliases(js.Value, []js.Value) interface{} {
	out := map[string]interface{}{}
	for name, pk := range resolverAliases {
		out[name] = pk.Hex()
	}
	return js.ValueOf(out)
}
