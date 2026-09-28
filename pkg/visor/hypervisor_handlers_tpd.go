// Package visor pkg/visor/hypervisor_handlers_tpd.go c3-vis-core
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
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

func (hv *Hypervisor) getNetworkTransports() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hv.visor == nil {
			httputil.WriteJSON(w, r, http.StatusServiceUnavailable,
				map[string]string{"error": "no local visor"})
			return
		}

		// Reducing 180k records takes a moment, and FetchTransportMetricsCXO
		// waits out the feed's first sync on a cold cache. Both happen before a
		// byte goes out, which the server's 10s WriteTimeout would cut short —
		// slow aggregating endpoints set their own deadline. See
		// hypervisor_handlers_visors.go for the same pattern.
		if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(120 * time.Second)); err != nil {
			hv.log(r).WithError(err).Debug("network transports: could not extend write deadline")
		}

		// Bound days to the TPD's documented range.
		days := 1
		if d := r.URL.Query().Get("days"); d != "" {
			if n, err := strconv.Atoi(d); err == nil && n >= 0 && n <= 35 {
				days = n
			}
		}

		limit := defaultMetricsLimit
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 {
				limit = n
			}
		}

		// The cap is applied after this filter, so "live only" means the top N
		// LIVE transports rather than whatever survives filtering the top N.
		liveOnly := r.URL.Query().Get("live") == "true"

		log := hv.visor.MasterLogger().PackageLogger("tpd_proxy")

		// CXO is the only path. The visor holds a long-lived subscriber to
		// TPD's metrics publisher, and FetchTransportMetricsCXO waits out the
		// feed's first sync when the cache is cold, so a miss here means the
		// feed genuinely has nothing yet — not that another transport might.
		//
		// There used to be a dmsg-HTTP fallback. It could not work and made
		// things worse: this payload is ~60 MB, one request pulls the whole
		// mesh, and the body came back truncated mid-record after about 30s.
		// All the fallback bought was another ~50s of waiting before the same
		// failure, turning a fast "not ready yet" into a two-minute hang.
		body, ts, err := hv.visor.FetchTransportMetricsCXO(days)
		if err != nil || len(body) == 0 {
			if err != nil {
				log.WithError(err).Debug("TPD metrics not in the CXO cache yet")
			}
			// 503 + Retry-After, not 502: nothing is broken, the feed is still
			// filling. The UI can say "warming up" and try again.
			w.Header().Set("Retry-After", "20")
			httputil.WriteJSON(w, r, http.StatusServiceUnavailable,
				map[string]string{"error": "tpd metrics feed is still syncing"})

			return
		}

		reduced, rerr := reduceTransportMetrics(body, limit, liveOnly)
		if rerr != nil {
			log.WithError(rerr).Warn("TPD metrics from CXO did not decode")
			httputil.WriteJSON(w, r, http.StatusBadGateway,
				map[string]string{"error": "tpd metrics unreadable"})

			return
		}
		if reduced.Partial {
			// The assembled feed ended mid-record. Returning what arrived beats
			// failing, and the flag lets the UI say the total is a floor.
			log.Warnf("TPD metrics truncated after %d records", reduced.Total)
		}

		if !ts.IsZero() {
			w.Header().Set("X-Skywire-Metrics-Updated", ts.UTC().Format(time.RFC3339))
		}
		w.Header().Set("X-Skywire-Metrics-Source", "cxo")
		httputil.WriteJSON(w, r, http.StatusOK, reduced)
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
			resp, err := hv.visor.DmsgHTTP(visorapi.DmsgHTTPRequest{URL: dmsgURL, Method: "GET"})
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
