// Package spec is the WASM-clean schema half of pkg/dmsgc. The types
// and methods here describe the visor's dmsg-subsystem configuration
// at the wire (JSON) layer only — no dmsg client construction, no
// network I/O, no transitive pull on operational packages.
//
// Why split: pkg/dmsgc itself imports pkg/dmsg/dmsg (the dmsg client
// implementation) which transitively brings in pty / ipc / bbolt
// vendor packages that don't compile under GOOS=js. That made
// pkg/visor/visorconfig.V1, which has a *dmsgc.DmsgConfig field,
// uncompilable from any WASM consumer (apt-repo install page, doc
// generators, schema validators).
//
// Moving the wire types out into this leaf package lets V1 reference
// dmsgcspec.DmsgConfig instead — V1 compiles under WASM, the
// install-page can render and emit live visor configs, and the
// operational pkg/dmsgc keeps doing its job for the visor binary
// via a `type DmsgConfig = spec.DmsgConfig` alias.
//
// All methods on DmsgConfig are pure: JSON marshalers and getters
// over the in-memory slice. None of them touch network, filesystem,
// or transitive operational packages. Adding an operational method
// (one that dials, fetches, etc.) belongs in pkg/dmsgc, not here.
package spec

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
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

// PKFromDmsgURL extracts the dmsg PK from a URL of the form
// `dmsg://<PK>:<port>[/path]`. Returns the zero PK when the URL is
// empty, malformed, or doesn't contain a valid PK in the host part.
//
// Exported (vs the pre-split lowercase pkFromDmsgURL) because the
// spec package is the natural home for parse helpers that depend
// only on stdlib + cipher. Internal callers in pkg/dmsgc now use
// spec.PKFromDmsgURL.
func PKFromDmsgURL(s string) cipher.PubKey {
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
