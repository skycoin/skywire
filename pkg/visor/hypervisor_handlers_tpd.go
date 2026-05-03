// Package visor pkg/visor/hypervisor_handlers_tpd.go
//
// Network-wide transport metrics proxy. Fetches the TPD's
// /metrics endpoint (which the `skywire-cli tp metrics` command
// also uses) and forwards the JSON to the hvui's Transports
// home tab. DMSG is preferred (uses the visor's DmsgHTTP),
// plain HTTP is the fallback when DMSG is unavailable or the
// TPD doesn't publish a DMSG address.
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

		tpdHTTP := strings.TrimSuffix(hv.visor.conf.Transport.Discovery, "/")
		tpdDmsg := strings.TrimSuffix(hv.visor.conf.Transport.DiscoveryDmsg, "/")
		if tpdHTTP == "" {
			tpdHTTP = strings.TrimSuffix(deployment.Prod.TransportDiscovery, "/")
		}
		if tpdDmsg == "" {
			tpdDmsg = strings.TrimSuffix(deployment.Prod.TransportDiscoveryDmsg, "/")
		}

		log := hv.visor.MasterLogger().PackageLogger("tpd_proxy")

		// Try DMSG first.
		if tpdDmsg != "" {
			dmsgURL := tpdDmsg + path
			log.Debugf("fetching TPD metrics via DMSG: %s", dmsgURL)
			resp, err := hv.visor.DmsgHTTP(DmsgHTTPRequest{
				URL:    dmsgURL,
				Method: "GET",
			})
			if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
				w.Header().Set("Content-Type", "application/json")
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

		// Plain-HTTP fallback. Used when DMSG isn't configured /
		// the visor's DMSG client isn't ready yet — same chain the
		// CLI uses via FetchServiceURL.
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
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(body) //nolint:errcheck,gosec
			return
		}

		httputil.WriteJSON(w, r, http.StatusServiceUnavailable,
			map[string]string{"error": "no TPD URL configured"})
	}
}
