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
