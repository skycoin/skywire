// Package api pkg/deployment/tpd/api/dmsg_roles.go
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cxo/cxosub"
	"github.com/skycoin/skywire/pkg/dmsg/discovery/serverfeed"
)

// Roles a visor can have as a dmsg server, for marking it on the charts page
// and the transport graph.
const (
	roleRegistered = "registered"
	roleLAN        = "lan"
)

const (
	dmsgRolesEvery   = 10 * time.Minute
	dmsgRolesTimeout = 30 * time.Second
)

// dmsgRoles knows which visors run a dmsg server: those with a server entry
// in dmsg discovery, and those clients delegate to without one, which are the
// servers hypervisors run for their LAN.
type dmsgRoles struct {
	client *http.Client
	base   string
	// feed, when set, is a subscription to dmsg discovery's clients-by-server
	// feed, read before asking over HTTP.
	feed *cxosub.Manager

	mu    sync.Mutex
	at    time.Time
	roles map[string]string
}

// SetDmsgDiscovery lets TPD ask dmsg discovery at url (dmsg://<pk>:<port>)
// which visors run dmsg servers, over client.
func (api *API) SetDmsgDiscovery(client *http.Client, url string) {
	api.dmsgRoles.Store(&dmsgRoles{client: client, base: "http://" + strings.TrimPrefix(url, "dmsg://")})
}

// serverRoles returns each dmsg server visor's role by key, from a copy at
// most dmsgRolesEvery old. It is empty when dmsg discovery is not known.
func (api *API) serverRoles(ctx context.Context) map[string]string {
	d := api.dmsgRoles.Load()
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if roles, ok := d.rolesFromFeed(); ok {
		return roles
	}
	if d.roles != nil && time.Since(d.at) < dmsgRolesEvery {
		return d.roles
	}
	ctx, cancel := context.WithTimeout(ctx, dmsgRolesTimeout)
	defer cancel()
	var servers []struct {
		Static string `json:"static"`
	}
	var byServer map[string][]string
	if err := d.get(ctx, "/dmsg-discovery/all_servers", &servers); err != nil {
		return d.roles
	}
	if err := d.get(ctx, "/dmsg-discovery/servers/clients", &byServer); err != nil {
		return d.roles
	}
	roles := make(map[string]string, len(servers)+len(byServer))
	for pk := range byServer {
		roles[pk] = roleLAN
	}
	for _, s := range servers {
		roles[s.Static] = roleRegistered
	}
	d.roles, d.at = roles, time.Now()
	return roles
}

func (d *dmsgRoles) get(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.base+path, nil)
	if err != nil {
		return err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// SetDmsgDiscoveryFeed has the role lookup read the servers from mgr's
// subscription to dmsg discovery's clients-by-server feed, falling back to
// HTTP while that has no servers. Call after SetDmsgDiscovery.
func (api *API) SetDmsgDiscoveryFeed(mgr *cxosub.Manager) {
	if d := api.dmsgRoles.Load(); d != nil {
		d.mu.Lock()
		d.feed = mgr
		d.mu.Unlock()
	}
}

// rolesFromFeed builds the roles from the feed, or reports false when it has
// no servers yet.
func (d *dmsgRoles) rolesFromFeed() (map[string]string, bool) {
	if d.feed == nil {
		return nil, false
	}
	servers, ok := serverfeed.Servers(d.feed)
	if !ok {
		return nil, false
	}
	roles := map[string]string{}
	for pk := range serverfeed.Delegated(d.feed) {
		roles[pk] = roleLAN
	}
	for _, s := range servers {
		roles[s.Static.Hex()] = roleRegistered
	}
	return roles, true
}
