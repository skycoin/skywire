// Package climdisc cmd/skywire-cli/commands/mdisc/check.go c2-ops-deployment
//
// `skywire cli mdisc check` — deployment health probe for the dmsg-server wss
// fronts. A browser/wasm visor can ONLY bootstrap over wss (the seed entry
// carries just AddressWS; WebTransport is a phase-2 trade-up whose ~2-week
// self-signed cert can't be pinned in advance), so every server that
// advertises an AddressWS must actually terminate it: correct DNS, valid TLS
// certificate, and the WebSocket endpoint answering 426 Upgrade Required to a
// plain HTTP/1.1 GET.
//
// This is the check that would have auto-caught the 2026-07-30 breakage where
// three servers advertised wss fronts that didn't serve: two with stale DNS
// pointing at a decommissioned shared proxy (dead :443) and one with a failed
// cert issuance — all masked, before the carrier-capability fix, as futile
// `dial tcp` errors on the client. Runs anywhere with clearnet https (CI
// included): the server list comes from the discovery's /all_servers endpoint
// and each probe is a plain HTTPS request.
package climdisc

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

var (
	checkTimeout  time.Duration
	checkWarnOnly bool
)

func init() {
	RootCmd.AddCommand(checkCmd)
	// --url is a persistent flag on the mdisc root (see root.go), so
	// `mdisc check --url ...` and `--testenv` are inherited here.
	checkCmd.Flags().DurationVar(&checkTimeout, "timeout", 15*time.Second, "per-server probe timeout")
	checkCmd.Flags().BoolVar(&checkWarnOnly, "warn-only", false, "always exit 0 (report problems without failing)")
}

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Probe every dmsg server's advertised wss front (DNS, TLS cert, 426)",
	Long: `Probe the health of every dmsg server registered in discovery.

For each server entry:
  - flags a missing AddressWS (the server is unreachable for browser/wasm
    visors, which can only bootstrap over wss);
  - resolves the wss host and warns when it doesn't cover the server's own
    IP (a legitimate shared TLS front also looks like this — warning only —
    but a STALE record pointing at a dead host is exactly how two servers
    silently dropped off the browser-reachable set);
  - performs a plain HTTP/1.1 GET of the endpoint with normal TLS
    verification, expecting 426 Upgrade Required (what a healthy unified
    dmsg-server WS front answers to a non-upgrade request).

Exits nonzero if any ADVERTISED wss front fails its probe, so it can gate a
scheduled CI job.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		entries, err := fetchAllServersAny(cmd, mdURL)
		if err != nil {
			return fmt.Errorf("fetch %s/dmsg-discovery/all_servers: %w", mdURL, err)
		}
		if len(entries) == 0 {
			return fmt.Errorf("discovery returned no dmsg servers")
		}

		failed := 0
		for _, e := range entries {
			if e.Server == nil {
				continue
			}
			label := e.Static.String()
			ws := e.Server.AddressWS
			if ws == "" {
				cmd.Printf("!  %s  %-21s  NO AddressWS — unreachable for browser/wasm visors\n", label, e.Server.Address)
				continue
			}
			if warn := dnsMismatch(ws, e.Server.Address); warn != "" {
				cmd.Printf("~  %s  %s\n", label, warn)
			}
			if perr := probeWS(ws, checkTimeout); perr != nil {
				failed++
				cmd.Printf("✗  %s  %-21s  %s: %v\n", label, e.Server.Address, ws, perr)
				continue
			}
			cmd.Printf("✓  %s  %-21s  %s\n", label, e.Server.Address, ws)
		}

		cmd.Printf("%d servers, %d advertised wss fronts failing\n", len(entries), failed)
		if failed > 0 && !checkWarnOnly {
			return fmt.Errorf("%d dmsg server(s) advertise a wss front that does not serve", failed)
		}
		return nil
	},
}

// fetchAllServersAny fetches the server list over whichever transport the
// discovery URL calls for: a dmsg:// URL (the dmsg-only deployment default)
// goes through the CLI's resolving fetch — same path the entries listing uses,
// needs a reachable visor; an http(s):// URL is fetched directly, which is
// what a CI box without a visor passes (e.g. --url https://dmsgd.skywire.dev).
func fetchAllServersAny(cmd *cobra.Command, base string) ([]*disc.Entry, error) {
	if strings.HasPrefix(base, "dmsg://") {
		full := strings.TrimRight(base, "/") + "/dmsg-discovery/all_servers"
		body := clirpc.FetchCachedServiceURL(cmd.Flags(), cacheFile(cacheDirDMSGD, full), full, cacheFilesAge)
		if strings.TrimSpace(body) == "" {
			return nil, fmt.Errorf("empty response over dmsg (no reachable visor? pass --url https://<clearnet discovery>)")
		}
		var entries []*disc.Entry
		if err := json.Unmarshal([]byte(body), &entries); err != nil {
			return nil, err
		}
		return entries, nil
	}
	return fetchAllServers(base)
}

// fetchAllServers reads the discovery's full server list over plain https —
// no dmsg client needed, so this runs on any CI box.
func fetchAllServers(base string) ([]*disc.Entry, error) {
	cl := &http.Client{Timeout: 30 * time.Second}
	resp, err := cl.Get(strings.TrimRight(base, "/") + "/dmsg-discovery/all_servers")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var entries []*disc.Entry
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// probeWS GETs the ws/wss endpoint as https/http over HTTP/1.1 (h2 rejects the
// WS Upgrade header a unified server emits, and real browser WebSocket dials
// are HTTP/1.1) with default TLS verification, expecting 426 Upgrade Required.
func probeWS(wsURL string, timeout time.Duration) error {
	httpURL := wsURL
	switch {
	case strings.HasPrefix(wsURL, "wss://"):
		httpURL = "https://" + strings.TrimPrefix(wsURL, "wss://")
	case strings.HasPrefix(wsURL, "ws://"):
		httpURL = "http://" + strings.TrimPrefix(wsURL, "ws://")
	}
	cl := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			// Force HTTP/1.1: an empty TLSNextProto disables h2 ALPN.
			TLSNextProto:      map[string]func(string, *tls.Conn) http.RoundTripper{},
			ForceAttemptHTTP2: false,
		},
	}
	resp, err := cl.Get(httpURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusUpgradeRequired {
		return fmt.Errorf("status %d, want 426 Upgrade Required", resp.StatusCode)
	}
	return nil
}

// dnsMismatch resolves the wss host and reports (as a warning string, "" for
// ok) when none of its addresses is the server's own advertised IP. A shared
// TLS front legitimately triggers this; a stale record pointing at a
// decommissioned host is the failure mode it exists to surface.
func dnsMismatch(wsURL, serverAddr string) string {
	u, err := url.Parse(strings.Replace(strings.Replace(wsURL, "wss://", "https://", 1), "ws://", "http://", 1))
	if err != nil || u.Hostname() == "" {
		return ""
	}
	serverHost, _, err := net.SplitHostPort(serverAddr)
	if err != nil {
		serverHost = serverAddr
	}
	ips, err := net.LookupHost(u.Hostname())
	if err != nil {
		return fmt.Sprintf("wss host %s does not resolve: %v", u.Hostname(), err)
	}
	for _, ip := range ips {
		if ip == serverHost {
			return ""
		}
	}
	return fmt.Sprintf("wss host %s resolves to %v — not the server's own address %s (shared front, or a stale record)", u.Hostname(), ips, serverHost)
}
