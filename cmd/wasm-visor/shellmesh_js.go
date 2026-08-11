//go:build js && wasm

// Package main cmd/wasm-visor/shellmesh_js.go c3-vis-wasm
// The mesh as the shell's network: fetch and dial by public key, over dmsg,
// from the terminal in the visor tab.
//
// websh already has curl and nc, but both are the browser's: fetch() and
// WebSocket, which reach clearnet origins and nothing else. What makes a shell
// in a browser tab interesting here is that the visor next to it can reach a
// keyless, PK-addressed overlay — so these applets route through the visor
// instead, and a public key is a hostname:
//
//	dcurl dmsg://<pk>:80/                    # or the resolver's aliases:
//	dcurl dmsg://tpd/security/nonces/<pk>
//	dcurl -s dmsg://rf/health | jq .
//	dial <pk>:80                             # is it reachable, and how fast
//
// # Capability
//
// These grant no reach the tab does not already have: the visor's in-tab
// browser resolves and fetches dmsg:// URLs through the same transport (see
// resolve_js.go), and the visor dials the same peers for its own transport and
// route lookups. This is that reach, in a shell, so it is gated by whether the
// visor booted rather than by a new switch.
//
// What WOULD be a new capability is executing on a remote peer — dmsgpty exec
// over the mesh. That path is deliberately absent here; it belongs behind the
// existing exec gate (visorconfig.Pty.AllowRPCExec, off by default) and is left
// for the milestone that adds it.
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/third_party/0magnet/sh/v3/interp"
	"github.com/skycoin/skywire/third_party/0magnet/websh/shell"
)

// meshHTTP is the dmsg-backed HTTP client, built once the visor has a dmsg
// client to build it from.
var (
	meshHTTPOnce sync.Once
	meshHTTPC    *http.Client
)

// meshClient returns an HTTP client whose transport is dmsg, or an error while
// the visor is still booting.
func meshClient() (*http.Client, error) {
	if dmsgC == nil {
		return nil, fmt.Errorf("the visor has no dmsg session yet")
	}
	meshHTTPOnce.Do(func() {
		meshHTTPC = &http.Client{
			Transport: dmsghttp.MakeHTTPTransport(context.Background(), dmsgC),
			Timeout:   30 * time.Second,
		}
	})
	return meshHTTPC, nil
}

// meshTarget is a parsed dmsg:// target: the peer to dial and the path to ask
// it for.
type meshTarget struct {
	pk   cipher.PubKey
	port uint16
	path string
}

// parseMeshTarget accepts dmsg://<pk-or-alias>[:port][/path], and the same
// without the scheme. Aliases are the resolver's (tpd, rf, sd, dmsg0, …), so
// the shell names services the way the rest of the visor does.
func parseMeshTarget(raw string) (meshTarget, error) {
	var t meshTarget
	t.port, t.path = 80, "/"

	s := raw
	for _, scheme := range []string{"dmsg://", "skynet://", "http://"} {
		if rest, ok := strings.CutPrefix(s, scheme); ok {
			s = rest
			break
		}
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		t.path, s = s[i:], s[:i]
	}
	host := s
	if i := strings.LastIndexByte(s, ':'); i >= 0 {
		port, err := strconv.ParseUint(s[i+1:], 10, 16)
		if err != nil {
			return t, fmt.Errorf("bad port in %q", raw)
		}
		t.port, host = uint16(port), s[:i]
	}
	host = strings.TrimSuffix(host, ".dmsg")
	if host == "" {
		return t, fmt.Errorf("no peer in %q", raw)
	}
	if pk, ok := resolverAliases[host]; ok {
		t.pk = pk
		return t, nil
	}
	if err := t.pk.Set(host); err != nil {
		return t, fmt.Errorf("%q is neither a public key nor a known alias", host)
	}
	return t, nil
}

// registerMeshApplets adds the mesh-backed network commands to the shell.
var registerMeshApplets = sync.OnceFunc(func() {
	shell.RegisterApplet("dcurl",
		"fetch over dmsg by public key: dcurl [-s] [-I] dmsg://<pk|alias>[:port][/path]",
		runDcurl)
	shell.RegisterApplet("dial",
		"check whether a peer is reachable over dmsg: dial <pk|alias>[:port]",
		runDial)
	shell.RegisterApplet("aliases",
		"list the resolver's service aliases and the public keys they name",
		runAliases)
})

// runDcurl implements the dcurl applet.
func runDcurl(ctx context.Context, _ *shell.Shell, hc *interp.HandlerContext, args []string) int {
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

	t, err := parseMeshTarget(target)
	if err != nil {
		_, _ = fmt.Fprintf(hc.Stderr, "dcurl: %v\n", err) //nolint:errcheck
		return 1
	}
	client, err := meshClient()
	if err != nil {
		_, _ = fmt.Fprintf(hc.Stderr, "dcurl: %v\n", err) //nolint:errcheck
		return 1
	}

	method := http.MethodGet
	if headOnly {
		method = http.MethodHead
	}
	// dmsghttp addresses a peer as <pk>:<port> in the URL host.
	url := fmt.Sprintf("http://%s:%d%s", t.pk.Hex(), t.port, t.path)
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		_, _ = fmt.Fprintf(hc.Stderr, "dcurl: %v\n", err) //nolint:errcheck
		return 1
	}
	resp, err := client.Do(req)
	if err != nil {
		_, _ = fmt.Fprintf(hc.Stderr, "dcurl: %v\n", err) //nolint:errcheck
		return 1
	}
	defer resp.Body.Close() //nolint:errcheck

	if !silent {
		_, _ = fmt.Fprintf(hc.Stderr, "%s %s\n", resp.Proto, resp.Status) //nolint:errcheck
	}
	if headOnly {
		for k, v := range resp.Header {
			_, _ = fmt.Fprintf(hc.Stdout, "%s: %s\n", k, strings.Join(v, ", ")) //nolint:errcheck
		}
		return 0
	}
	if _, err := io.Copy(hc.Stdout, resp.Body); err != nil {
		_, _ = fmt.Fprintf(hc.Stderr, "dcurl: %v\n", err) //nolint:errcheck
		return 1
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 1
	}
	return 0
}

// runDial implements the dial applet: open a dmsg stream to a peer, report
// whether it came up and how long it took, then close it. It is the mesh's
// ping — reachability by public key, without needing the peer to speak HTTP.
func runDial(ctx context.Context, _ *shell.Shell, hc *interp.HandlerContext, args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(hc.Stderr, "usage: dial <pk|alias>[:port]") //nolint:errcheck
		return 2
	}
	t, err := parseMeshTarget(args[0])
	if err != nil {
		_, _ = fmt.Fprintf(hc.Stderr, "dial: %v\n", err) //nolint:errcheck
		return 1
	}
	if dmsgC == nil {
		_, _ = fmt.Fprintln(hc.Stderr, "dial: the visor has no dmsg session yet") //nolint:errcheck
		return 1
	}

	start := time.Now()
	stream, err := dmsgC.DialStream(ctx, dmsg.Addr{PK: t.pk, Port: t.port})
	if err != nil {
		_, _ = fmt.Fprintf(hc.Stderr, "dial: %s:%d unreachable: %v\n", t.pk.Hex()[:8], t.port, err) //nolint:errcheck
		return 1
	}
	elapsed := time.Since(start)
	_ = stream.Close()                                                                                            //nolint:errcheck
	_, _ = fmt.Fprintf(hc.Stdout, "%s:%d reachable in %s\n", t.pk.Hex(), t.port, elapsed.Round(time.Millisecond)) //nolint:errcheck
	return 0
}

// runAliases prints the resolver's alias table, so a user can discover what
// names dcurl and dial accept without knowing the deployment's keys.
func runAliases(_ context.Context, _ *shell.Shell, hc *interp.HandlerContext, _ []string) int {
	if len(resolverAliases) == 0 {
		_, _ = fmt.Fprintln(hc.Stderr, "no aliases yet — the visor resolves them once it has its service set") //nolint:errcheck
		return 1
	}
	names := make([]string, 0, len(resolverAliases))
	for name := range resolverAliases {
		names = append(names, name)
	}
	sortStrings(names)
	for _, name := range names {
		_, _ = fmt.Fprintf(hc.Stdout, "%-8s %s\n", name, resolverAliases[name].Hex()) //nolint:errcheck
	}
	return 0
}

// sortStrings is a tiny insertion sort; the alias table is a dozen entries and
// this keeps the file free of a sort import for one call.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
