// Package visor pkg/visor/hypervisor_handlers_tpd.go
//
// Network-wide transport metrics proxy for the hvui's Transports
// home tab. Three fetch strategies are tried in order:
//
//  1. CXO subscriber (instant when fresh) — the visor maintains a
//     long-lived TreeStore subscriber to TPD's metrics-aggregate
//     publisher (see api_tpd_metrics_subscriber.go). When the
//     publisher has pushed a Root for the requested day window the
//     subscriber's local cache returns it immediately, no DMSG
//     round-trip per hvui open.
//  2. DMSG-HTTP — bypass the CXO path and ask TPD for /metrics over
//     dmsghttp through the visor's existing DmsgHTTP RPC.
//  3. Plain HTTP fallback — last resort when DMSG isn't ready /
//     TPD doesn't publish a DMSG address.
//
// The first strategy that returns 2xx wins; later strategies aren't
// tried unless the earlier one explicitly missed.
package visor

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/httputil"
)

func (hv *Hypervisor) getNetworkTransports() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hv.visor == nil {
			httputil.WriteJSON(w, r, http.StatusServiceUnavailable,
				map[string]string{"error": "no local visor"})
			return
		}

		// Bound days to the TPD's documented range.
		days := 1
		if d := r.URL.Query().Get("days"); d != "" {
			if n, err := strconv.Atoi(d); err == nil && n >= 0 && n <= 35 {
				days = n
			}
		}
		path := fmt.Sprintf("/metrics?days=%d&bandwidth=true&latency=true&edges=true", days)

		log := hv.visor.MasterLogger().PackageLogger("tpd_proxy")

		// Step 1: CXO subscriber cache. Hits when TPD has pushed a
		// Root for this day window since visor startup. The header
		// X-Skywire-Metrics-Source = cxo lets the UI surface the
		// path used (handy for diagnosing slow loads).
		if body, ts, err := hv.visor.FetchTransportMetricsCXO(days); err == nil && len(body) > 0 {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Skywire-Metrics-Source", "cxo")
			if !ts.IsZero() {
				w.Header().Set("X-Skywire-Metrics-Updated", ts.UTC().Format(time.RFC3339))
			}
			_, _ = w.Write(body) //nolint:errcheck,gosec
			return
		}

		tpdHTTP := strings.TrimSuffix(hv.visor.conf.Transport.Discovery, "/")
		tpdDmsg := strings.TrimSuffix(hv.visor.conf.Transport.DiscoveryDmsg, "/")
		if tpdHTTP == "" {
			tpdHTTP = strings.TrimSuffix(deployment.Prod.TransportDiscovery, "/")
		}
		if tpdDmsg == "" {
			tpdDmsg = strings.TrimSuffix(deployment.Prod.TransportDiscoveryDmsg, "/")
		}

		// Step 2: DMSG-HTTP via the visor's DmsgHTTP RPC.
		if tpdDmsg != "" {
			dmsgURL := tpdDmsg + path
			log.Debugf("fetching TPD metrics via DMSG: %s", dmsgURL)
			resp, err := hv.visor.DmsgHTTP(DmsgHTTPRequest{
				URL:    dmsgURL,
				Method: "GET",
			})
			if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Skywire-Metrics-Source", "dmsg-http")
				w.WriteHeader(resp.StatusCode)
				_, _ = w.Write(resp.Body) //nolint:errcheck,gosec
				return
			}
			if err != nil {
				log.WithError(err).Warn("DMSG fetch failed, falling back to HTTP")
			} else {
				log.Warnf("DMSG fetch returned %d, falling back to HTTP", resp.StatusCode)
			}
		}

		// Step 3: plain HTTP. Same chain the CLI's FetchServiceURL uses.
		if tpdHTTP != "" {
			httpURL := tpdHTTP + path
			log.Debugf("fetching TPD metrics via HTTP: %s", httpURL)
			client := &http.Client{Timeout: 15 * time.Second}
			resp, err := client.Get(httpURL) //nolint:gosec // operator-controlled URL from visor config
			if err != nil {
				httputil.WriteJSON(w, r, http.StatusBadGateway,
					map[string]string{"error": "tpd unreachable: " + err.Error()})
				return
			}
			defer resp.Body.Close()          //nolint:errcheck
			body, _ := io.ReadAll(resp.Body) //nolint:errcheck
			w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
			w.Header().Set("X-Skywire-Metrics-Source", "http")
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(body) //nolint:errcheck,gosec
			return
		}

		httputil.WriteJSON(w, r, http.StatusServiceUnavailable,
			map[string]string{"error": "no TPD URL configured"})
	}
}

// getNetworkVisorUptime proxies TPD's `/uptimes?v=v3` for the
// network-wide Uptime tab. The TPD aggregates visor heartbeats from
// the integrated tracker (transports + dmsg discovery check-ins);
// the v3 response includes a 288-char per-day timeline string per
// visor — exactly the "exact intervals" shape the operator wants.
//
// Three fetch strategies, tried in order:
//
//  1. CXO subscriber (instant when fresh) — visor maintains a lazy
//     long-lived TreeStore subscriber to TPD's uptime publisher
//     (api_tpd_uptime_subscriber.go). When the publisher has pushed
//     a Root for the requested day window the subscriber returns
//     it without a DMSG round-trip per hvui open.
//  2. DMSG-HTTP fallback when the CXO cache misses.
//  3. Plain HTTP last resort.
//
// Query params:
//
//	days=N             — selects which CXO bucket to read (1, 7, 30);
//	                     defaults to 7. Only the publisher-supported
//	                     windows hit the cache; everything else falls
//	                     straight through to the HTTP path.
//	visors=<pk>;<pk>... — semicolon-separated PK filter; only takes
//	                     effect on the HTTP/DMSG-HTTP fallback path
//	                     (the CXO bucket is the full fleet for that
//	                     window — clients filter on their side).
func (hv *Hypervisor) getNetworkVisorUptime() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hv.visor == nil {
			httputil.WriteJSON(w, r, http.StatusServiceUnavailable,
				map[string]string{"error": "no local visor"})
			return
		}

		visorsParam := strings.TrimSpace(r.URL.Query().Get("visors"))
		// Default to v3 — the timeline is what the UI renders.
		// Callers that just want percentages can pass v=v2.
		version := r.URL.Query().Get("v")
		if version == "" {
			version = "v3"
		}
		// Parse days for the CXO bucket lookup. If unparseable or
		// missing, default to 7 — matches the per-visor tab default.
		days := 7
		if d := strings.TrimSpace(r.URL.Query().Get("days")); d != "" {
			if n, err := strconv.Atoi(d); err == nil && n > 0 && n <= 35 {
				days = n
			}
		}

		log := hv.visor.MasterLogger().PackageLogger("tpd_uptime_proxy")

		// Step 1: CXO subscriber bucket. Hits whenever TPD has pushed
		// the requested day window since the visor's first hvui-driven
		// fetch. The X-Skywire-Uptime-Source header lets the UI know
		// where the response came from.
		if version == "v3" {
			if body, ts, err := hv.visor.FetchVisorUptimeCXO(days); err == nil && len(body) > 0 {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Skywire-Uptime-Source", "cxo")
				if !ts.IsZero() {
					w.Header().Set("X-Skywire-Uptime-Updated", ts.UTC().Format(time.RFC3339))
				}
				_, _ = w.Write(body) //nolint:errcheck,gosec
				return
			}
		}

		path := "/uptimes?v=" + version
		if visorsParam != "" {
			path += "&visors=" + visorsParam
		}

		tpdHTTP := strings.TrimSuffix(hv.visor.conf.Transport.Discovery, "/")
		tpdDmsg := strings.TrimSuffix(hv.visor.conf.Transport.DiscoveryDmsg, "/")
		if tpdHTTP == "" {
			tpdHTTP = strings.TrimSuffix(deployment.Prod.TransportDiscovery, "/")
		}
		if tpdDmsg == "" {
			tpdDmsg = strings.TrimSuffix(deployment.Prod.TransportDiscoveryDmsg, "/")
		}

		if tpdDmsg != "" {
			dmsgURL := tpdDmsg + path
			log.Debugf("fetching TPD /uptimes via DMSG: %s", dmsgURL)
			resp, err := hv.visor.DmsgHTTP(DmsgHTTPRequest{URL: dmsgURL, Method: "GET"})
			if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Skywire-Uptime-Source", "dmsg-http")
				w.WriteHeader(resp.StatusCode)
				_, _ = w.Write(resp.Body) //nolint:errcheck,gosec
				return
			}
			if err != nil {
				log.WithError(err).Warn("DMSG fetch failed, falling back to HTTP")
			}
		}

		if tpdHTTP != "" {
			httpURL := tpdHTTP + path
			log.Debugf("fetching TPD /uptimes via HTTP: %s", httpURL)
			client := &http.Client{Timeout: 15 * time.Second}
			resp, err := client.Get(httpURL) //nolint:gosec // operator-controlled URL
			if err != nil {
				httputil.WriteJSON(w, r, http.StatusBadGateway,
					map[string]string{"error": "tpd unreachable: " + err.Error()})
				return
			}
			defer resp.Body.Close()          //nolint:errcheck
			body, _ := io.ReadAll(resp.Body) //nolint:errcheck
			w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
			w.Header().Set("X-Skywire-Uptime-Source", "http")
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(body) //nolint:errcheck,gosec
			return
		}

		httputil.WriteJSON(w, r, http.StatusServiceUnavailable,
			map[string]string{"error": "no TPD URL configured"})
	}
}
