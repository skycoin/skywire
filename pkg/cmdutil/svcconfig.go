// Package cmdutil pkg/cmdutil/svcconfig.go
package cmdutil

import (
	"net/url"
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

// DmsgConfig is the shared dmsg-related block embedded in each
// deployment service's config file. Mirrors the visor's
// `dmsg.discovery` / `dmsg.discovery_dmsg` / `dmsg.servers` shape so
// operators see a consistent schema across visor + every deployment
// service. The discovery's PK is encoded inside DiscoveryDmsg
// (`dmsg://<PK>:<port>`); there is no separate PK field — see
// PKFromDmsgURL.
//
// Polymorphism (object vs array) is intentionally NOT supported here
// — deployment services don't have a multi-deployment use case the
// way the visor does. If that ever changes, the visor's
// pkg/dmsgc.DmsgConfig is the precedent.
type DmsgConfig struct {
	// Discovery is the HTTP URL of the dmsg-discovery the service
	// uses for its own dmsg-client lifecycle (refreshing the server
	// list, registering a client entry, etc.). Empty for services
	// that don't talk to a discovery (dmsg-discovery itself).
	Discovery string `json:"discovery,omitempty"`
	// DiscoveryDmsg is the dmsg-HTTP URL of the same discovery,
	// form `dmsg://<PK>:<port>`. Used by dmsgfirst-style clients
	// to dial the discovery over dmsg before falling back to HTTP.
	DiscoveryDmsg string `json:"discovery_dmsg,omitempty"`
	// SessionsCount is the minimum number of dmsg-server sessions
	// the service should maintain. Zero leaves it to the runtime
	// default (typically 0 — connect to all available servers).
	SessionsCount int `json:"sessions_count,omitempty"`
	// ServerType filters dmsg-servers by their declared type. Empty
	// matches all. Mirrors the existing --dmsg-server-type flag.
	ServerType string `json:"server_type,omitempty"`
	// Servers is the static dmsg-server transit set the service
	// preloads at startup. Replaces direct reads of
	// dmsg.Prod.DmsgServers at runtime — operators ship a config
	// file generated from the embedded keyring once and edit it as
	// servers rotate, no rebuild required.
	Servers []*disc.Entry `json:"servers,omitempty"`
}

// PKFromDmsgURL extracts the dmsg PK from a URL of the form
// `dmsg://<PK>:<port>[/path]`. Returns the zero PK when the URL is
// empty, malformed, or doesn't carry a valid PK in the host part.
// Used by callers that need the discovery's PK for dmsgfirst-style
// dials without forcing the operator to repeat the PK as a separate
// field in config.
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
