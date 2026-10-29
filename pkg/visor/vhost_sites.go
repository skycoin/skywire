package visor

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// caddyServers is the part of Caddy's admin API config that names its sites.
// https://caddyserver.com/docs/api#get-configpath
const caddyServers = "http://127.0.0.1:2019/config/apps/http/servers"

const siteListTTL = 5 * time.Minute

// siteCache holds the site names Caddy reported last.
type siteCache struct {
	mu   sync.Mutex
	list []string
	at   time.Time
}

func (c *siteCache) get(log *logging.Logger) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && time.Since(c.at) < siteListTTL {
		return c.list
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	list, err := caddySites(ctx, caddyServers)
	if err != nil {
		log.WithError(err).Debug("Could not read the site list from Caddy")
	} else {
		c.list = list
	}
	c.at = time.Now()
	return c.list
}

// caddySites are the host names the routes of Caddy's servers match.
func caddySites(ctx context.Context, url string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("caddy admin: %s", resp.Status)
	}
	var servers map[string]struct {
		Routes []struct {
			Match []struct {
				Host []string `json:"host"`
			} `json:"match"`
		} `json:"routes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&servers); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, s := range servers {
		for _, r := range s.Routes {
			for _, m := range r.Match {
				for _, h := range m.Host {
					if h = strings.ToLower(h); isSiteName(h) {
						seen[h] = true
					}
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	sort.Strings(out)
	return out, nil
}

// isSiteName reports whether h names one site that can be linked: not a
// wildcard, an address, localhost, or a name for one visor by its key.
func isSiteName(h string) bool {
	if h == "" || strings.Contains(h, "*") || h == "localhost" || net.ParseIP(h) != nil {
		return false
	}
	first, _, _ := strings.Cut(h, ".")
	var pk cipher.PubKey
	return pk.Set(first) != nil
}

// vhostSiteList is the sites a vhost links to: those it lists, and for a
// catch-all that hands the Host to a local server, the sites Caddy reports.
func (v *Visor) vhostSiteList(vh visorapi.Vhost, target string, log *logging.Logger) func() []string {
	caddy := v.localSiteList(vh.Name == "*" && vh.PreserveHost, target, log)
	if caddy == nil {
		if len(vh.Sites) == 0 {
			return nil
		}
		return func() []string { return vh.Sites }
	}
	return func() []string {
		return append(append([]string(nil), vh.Sites...), caddy()...)
	}
}

// localSiteList is the sites Caddy reports, for a forward that hands every
// name with its Host to a server on this machine. Otherwise nil.
func (v *Visor) localSiteList(takesEveryName bool, target string, log *logging.Logger) func() []string {
	if !takesEveryName || !isLoopback(target) {
		return nil
	}
	return func() []string { return v.caddySites.get(log) }
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// serveOwnPages serves the visor's own pages over conn, for a forwarded port
// set to show the landing page.
func (v *Visor) serveOwnPages(conn net.Conn) {
	v.initLock.RLock()
	lsAPI := v.logServer.api
	v.initLock.RUnlock()
	if lsAPI == nil {
		return
	}
	lis := newSingleConnListener(conn)
	srv := &http.Server{
		Handler:           lsAPI.OwnPages(),
		ReadHeaderTimeout: 30 * time.Second,
		ConnState: func(_ net.Conn, s http.ConnState) {
			if s == http.StateClosed || s == http.StateHijacked {
				_ = lis.Close() //nolint:errcheck
			}
		},
	}
	_ = srv.Serve(lis) //nolint:errcheck
}
