// Package dmsgc dmsg config and client
package dmsgc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// Deployment is one (dmsg-discovery, transit-servers) pair. Each
// Deployment is independent — different deployments may use different
// servers, sessions counts, protocols, or LAN preferences.
//
// The discovery's PK is encoded in DiscoveryDmsg (form
// `dmsg://<PK>:<port>`) and is extracted on demand by callers that
// need it for dmsgfirst registration; there is no separate PK field.
type Deployment struct {
	Discovery            string        `json:"discovery,omitempty"`
	DiscoveryDmsg        string        `json:"discovery_dmsg,omitempty"`
	SessionsCount        int           `json:"sessions_count,omitempty"`
	Servers              []*disc.Entry `json:"servers,omitempty"`
	ConnectedServersType string        `json:"servers_type,omitempty"`
	Protocol             string        `json:"protocol,omitempty"`
	LANServers           []*disc.Entry `json:"lan_servers,omitempty"`
	// HypervisorDiscovery is an optional override URL pointing at a
	// hypervisor-hosted dmsg-discovery proxy. When set, the dmsg client
	// queries this URL first and falls back to Discovery (the canonical
	// public dmsg-discovery) on error. Pushed automatically by
	// hypervisors with their embedded dmsg server enabled — see
	// LANDmsgServerInfo.DiscoveryURL.
	HypervisorDiscovery string `json:"hypervisor_discovery,omitempty"`
}

// DmsgConfig is the visor-side dmsg subsystem configuration.
//
// JSON polymorphism: a single Deployment object is the single-
// deployment shape that visor configs have always used; an array of
// Deployments is the multi-deployment shape, where each entry pairs
// a dmsg-discovery with the dmsg-servers that reach it. Internally
// the type stores a slice; the single-object shape unmarshals into
// a one-element slice and marshals back to an object.
//
// For backward compatibility with code that reads
// `v.conf.Dmsg.Discovery` etc. directly, the legacy top-level fields
// are kept as a mirror of Deployments[0]. Writers that build a
// DmsgConfig by setting top-level fields directly still work — the
// MarshalJSON synthesizes a one-element Deployments from the
// top-level fields when Deployments is empty.
type DmsgConfig struct {
	// Deployments is the canonical list, populated by UnmarshalJSON.
	Deployments []Deployment `json:"-"`

	// Top-level mirror fields. Read-only after UnmarshalJSON; the
	// canonical state lives in Deployments. Kept to avoid churning
	// the many existing call sites that read Dmsg.Discovery /
	// Dmsg.DiscoveryDmsg / Dmsg.Servers / etc. directly.
	Discovery            string        `json:"-"`
	DiscoveryDmsg        string        `json:"-"`
	SessionsCount        int           `json:"-"`
	Servers              []*disc.Entry `json:"-"`
	ConnectedServersType string        `json:"-"`
	Protocol             string        `json:"-"`
	LANServers           []*disc.Entry `json:"-"`
	HypervisorDiscovery  string        `json:"-"`
}

// UnmarshalJSON accepts either a single Deployment object or an array
// of Deployments and populates Deployments + the legacy top-level
// mirror fields accordingly.
func (c *DmsgConfig) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimLeft(data, " \t\n\r")
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(data, &c.Deployments); err != nil {
			return err
		}
	} else {
		var single Deployment
		if err := json.Unmarshal(data, &single); err != nil {
			return err
		}
		c.Deployments = []Deployment{single}
	}
	c.mirrorPrimary()
	return nil
}

// MarshalJSON emits the single-deployment object shape when there is
// exactly one deployment, otherwise the array shape. When Deployments
// is empty but the top-level fields are populated (e.g. a writer that
// only set Discovery + Servers directly), a one-element Deployments
// is synthesized from the mirror fields so the JSON output is correct.
func (c DmsgConfig) MarshalJSON() ([]byte, error) {
	deployments := c.Deployments
	if len(deployments) == 0 && (c.Discovery != "" || c.DiscoveryDmsg != "" || len(c.Servers) > 0 || c.SessionsCount != 0 || c.ConnectedServersType != "" || c.Protocol != "" || len(c.LANServers) > 0 || c.HypervisorDiscovery != "") {
		deployments = []Deployment{c.toDeployment()}
	}
	if len(deployments) == 1 {
		return json.Marshal(deployments[0])
	}
	return json.Marshal(deployments)
}

// mirrorPrimary copies Deployments[0] into the legacy top-level fields
// so existing readers keep working in the single-deployment case. For
// multi-deployment configs the mirror reflects the first entry — code
// that needs to iterate uses Deployments directly.
func (c *DmsgConfig) mirrorPrimary() {
	if len(c.Deployments) == 0 {
		return
	}
	d := c.Deployments[0]
	c.Discovery = d.Discovery
	c.DiscoveryDmsg = d.DiscoveryDmsg
	c.SessionsCount = d.SessionsCount
	c.Servers = d.Servers
	c.ConnectedServersType = d.ConnectedServersType
	c.Protocol = d.Protocol
	c.LANServers = d.LANServers
	c.HypervisorDiscovery = d.HypervisorDiscovery
}

// toDeployment snapshots the legacy top-level fields into a Deployment.
func (c *DmsgConfig) toDeployment() Deployment {
	return Deployment{
		Discovery:            c.Discovery,
		DiscoveryDmsg:        c.DiscoveryDmsg,
		SessionsCount:        c.SessionsCount,
		Servers:              c.Servers,
		ConnectedServersType: c.ConnectedServersType,
		Protocol:             c.Protocol,
		LANServers:           c.LANServers,
		HypervisorDiscovery:  c.HypervisorDiscovery,
	}
}

// Primary returns the first deployment, synthesizing one from the
// legacy top-level fields when Deployments is empty. Callers can rely
// on Primary always returning a non-nil pointer for any non-empty
// DmsgConfig.
func (c *DmsgConfig) Primary() *Deployment {
	if len(c.Deployments) > 0 {
		return &c.Deployments[0]
	}
	d := c.toDeployment()
	return &d
}

// AllDeployments returns the list of deployments, synthesizing a
// one-element list from the legacy top-level fields when Deployments
// is empty. Returns nil only when the config is fully zero.
func (c *DmsgConfig) AllDeployments() []Deployment {
	if len(c.Deployments) > 0 {
		return c.Deployments
	}
	if c.Discovery == "" && c.DiscoveryDmsg == "" && len(c.Servers) == 0 && c.HypervisorDiscovery == "" {
		return nil
	}
	return []Deployment{c.toDeployment()}
}

// ResolvedServers unions servers from all deployments, deduping by PK.
// The result is what the direct.Client should be preloaded with so the
// client can dmsg-dial every configured discovery — whether
// deployments share a server set or have disjoint per-deployment sets.
func (c *DmsgConfig) ResolvedServers() []*disc.Entry {
	seen := make(map[cipher.PubKey]struct{})
	var out []*disc.Entry
	add := func(e *disc.Entry) {
		if e == nil {
			return
		}
		if _, ok := seen[e.Static]; ok {
			return
		}
		seen[e.Static] = struct{}{}
		out = append(out, e)
	}
	for _, d := range c.AllDeployments() {
		for _, e := range d.Servers {
			add(e)
		}
	}
	return out
}

// pkFromDmsgURL extracts the dmsg PK from a URL of the form
// `dmsg://<PK>:<port>[/path]`. Returns the zero PK when the URL is
// empty, malformed, or doesn't contain a valid PK in the host part.
func pkFromDmsgURL(s string) cipher.PubKey {
	if s == "" {
		return cipher.PubKey{}
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return cipher.PubKey{}
	}
	host := u.Hostname()
	if host == "" {
		host = strings.SplitN(u.Host, ":", 2)[0]
	}
	var pk cipher.PubKey
	if err := pk.Set(host); err != nil {
		return cipher.PubKey{}
	}
	return pk
}

// lanPriorityDisc wraps a disc.APIClient to prepend LAN server entries
// to discovery results. LAN servers are tried first, with automatic
// fallback to public servers if the LAN server is unreachable.
type lanPriorityDisc struct {
	disc.APIClient
	lanEntries []*disc.Entry
}

func (d *lanPriorityDisc) AvailableServers(ctx context.Context) ([]*disc.Entry, error) {
	entries, err := d.APIClient.AvailableServers(ctx)
	if err != nil {
		if len(d.lanEntries) > 0 {
			return d.lanEntries, nil
		}
		return nil, err
	}
	return append(d.lanEntries, entries...), nil
}

func (d *lanPriorityDisc) AllServers(ctx context.Context) ([]*disc.Entry, error) {
	entries, err := d.APIClient.AllServers(ctx)
	if err != nil {
		if len(d.lanEntries) > 0 {
			return d.lanEntries, nil
		}
		return nil, err
	}
	return append(d.lanEntries, entries...), nil
}

// New makes new dmsg client from configuration
func New(pk cipher.PubKey, sk cipher.SecKey, eb *appevent.Broadcaster, conf *DmsgConfig, httpC *http.Client, masterLogger *logging.MasterLogger) *dmsg.Client {
	primary := conf.Primary()
	dmsgConf := &dmsg.Config{
		MinSessions: primary.SessionsCount,
		Callbacks: &dmsg.ClientCallbacks{
			OnSessionDial: func(network, addr string) error {
				data := appevent.TCPDialData{RemoteNet: network, RemoteAddr: addr}
				event := appevent.NewEvent(appevent.TCPDial, data)
				_ = eb.Broadcast(context.Background(), event) //nolint:errcheck
				// @evanlinjin: An error is not returned here as this will cancel the session dial.
				return nil
			},
			OnSessionDisconnect: func(network, addr string, _ error) {
				data := appevent.TCPCloseData{RemoteNet: network, RemoteAddr: addr}
				event := appevent.NewEvent(appevent.TCPClose, data)
				_ = eb.Broadcast(context.Background(), event) //nolint:errcheck
			},
		},
		ConnectedServersType: primary.ConnectedServersType,
		Protocol:             primary.Protocol,
	}
	dmsgConf.ClientType = "visor"

	deployments := conf.AllDeployments()
	if len(deployments) == 0 {
		deployments = []Deployment{{}}
	}

	primaryC := disc.NewHTTP(deployments[0].Discovery, httpC, masterLogger.PackageLogger("dmsgC:disc"))
	// When the operator (or a hypervisor's auto-discovery push) has set
	// HypervisorDiscovery, wrap the public discovery as the fallback
	// behind it. The hypervisor's proxy serves locally-known PKs from
	// its embedded dmsg server's session list and forwards everything
	// else upstream — but if the hypervisor itself is unreachable, the
	// fallback ensures lookups still hit public discovery directly
	// instead of failing outright.
	if deployments[0].HypervisorDiscovery != "" {
		hypC := disc.NewHTTP(deployments[0].HypervisorDiscovery, httpC, masterLogger.PackageLogger("dmsgC:disc:hypervisor"))
		masterLogger.PackageLogger("dmsgC").
			Infof("Using hypervisor dmsg-discovery proxy as primary: %s (public fallback: %s)",
				deployments[0].HypervisorDiscovery, deployments[0].Discovery)
		primaryC = disc.NewFallbackClient(hypC, primaryC, masterLogger.PackageLogger("dmsgC:disc:fallback"))
	}
	if len(primary.LANServers) > 0 {
		masterLogger.PackageLogger("dmsgC").Infof("Using %d LAN DMSG servers (tried first)", len(primary.LANServers))
		primaryC = &lanPriorityDisc{APIClient: primaryC, lanEntries: primary.LANServers}
	}

	dmsgC := dmsg.NewClient(pk, sk, primaryC, dmsgConf)
	dmsgC.SetLogger(masterLogger.PackageLogger("dmsgC"))
	dmsgC.SetMasterLogger(masterLogger)

	// Pre-seed the entry cache with every configured server (across
	// all deployments + LAN servers). EnsureAndObtainSession resolves
	// server entries from this cache before falling through to the
	// disc client, which prevents a recursion that otherwise blows the
	// goroutine stack on shutdown / disrupted-network paths: when
	// ce.dc is the dmsgfirst wrapper, its primary is dmsghttp, and
	// dmsghttp.RoundTrip → DialStream → EnsureAndObtainSession →
	// getServerEntry → ce.dc.Entry → dmsghttp.RoundTrip … with
	// nothing to break the loop. With these entries pre-seeded, any
	// dial against a configured server short-circuits before the disc
	// client is even consulted, so the loop never starts.
	seenSeeds := make(map[cipher.PubKey]struct{})
	seedFn := func(srcLabel string, entries []*disc.Entry) {
		for _, srv := range entries {
			if srv == nil || srv.Static.Null() || srv.Server == nil {
				continue
			}
			if _, dup := seenSeeds[srv.Static]; dup {
				continue
			}
			seenSeeds[srv.Static] = struct{}{}
			dmsgC.SeedEntryCache(srv.Static, srv)
			masterLogger.PackageLogger("dmsgC").
				WithField("server_pk", srv.Static.String()).
				WithField("source", srcLabel).
				Debug("Pre-seeded dmsg server entry into cache")
		}
	}
	for i, dep := range deployments {
		seedFn(fmt.Sprintf("deployments[%d].servers", i), dep.Servers)
	}
	seedFn("primary.lan_servers", primary.LANServers)
	// Attach extra deployments so the visor publishes its client entry
	// to every configured discovery. Plain HTTP at construction time;
	// the visor's init_dmsg path can swap these for dmsgfirst-wrapped
	// clients once dmsgC has a usable session.
	for i := 1; i < len(deployments); i++ {
		dmsgC.AddDiscovery(
			disc.NewHTTP(deployments[i].Discovery, httpC, masterLogger.PackageLogger("dmsgC:disc")),
			pkFromDmsgURL(deployments[i].DiscoveryDmsg),
		)
	}
	return dmsgC
}
