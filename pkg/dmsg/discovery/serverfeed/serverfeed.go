// Package serverfeed reads the dmsg servers dmsg discovery publishes on its
// clients-by-server CXO feed, for the visor's server cache, the dmsg servers'
// peer discovery and TPD's marking of dmsg server visors.
//
// Registered servers are leaves at clients-by-server/servers/<pk>, each the
// server's signed entry, version-framed and gzipped. Every server with at
// least one delegated client has a batch leaf at clients-by-server/<pk>; one
// of those with no registered entry is a server a hypervisor runs for its LAN.
package serverfeed

import (
	"encoding/json"
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxosub"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

const (
	// Prefix is the clients-by-server feed's prefix.
	Prefix = "clients-by-server/"
	// ServerPrefix is where the registered servers' leaves are.
	ServerPrefix = Prefix + "servers/"
	// Version is the server leaf's frame version.
	Version = 1
)

// LeafPath is a registered server's leaf.
func LeafPath(pk cipher.PubKey) string { return ServerPrefix + pk.Hex() }

// Encode frames a server entry as a leaf body.
func Encode(e *disc.Entry) ([]byte, error) {
	body, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	return cxoutils.FrameGzip(Version, body), nil
}

// Decode reads a server leaf, or returns nil when it is not one this build
// understands.
func Decode(body []byte) *disc.Entry {
	v, payload, ok := cxoutils.UnframeGzip(body)
	if !ok || v != Version {
		return nil
	}
	e := new(disc.Entry)
	if err := json.Unmarshal(payload, e); err != nil || e.Server == nil {
		return nil
	}
	return e
}

// Servers returns the registered servers in mgr's snapshot of the feed. ok is
// false when there is no snapshot yet or it has no server leaves, as from a
// dmsg discovery older than the server leaves; the caller then asks over HTTP.
func Servers(mgr *cxosub.Manager) (servers []*disc.Entry, ok bool) {
	if mgr == nil || mgr.LastSync(cxosub.FeedDMSGDClientsByServer).IsZero() {
		return nil, false
	}
	mgr.Walk(cxosub.FeedDMSGDClientsByServer, ServerPrefix, func(path string, body []byte) bool {
		if strings.Contains(strings.TrimPrefix(path, ServerPrefix), "/") {
			return true
		}
		if e := Decode(body); e != nil {
			servers = append(servers, e)
		}
		return true
	})
	return servers, len(servers) > 0
}

// Delegated returns the keys, in hex, of every server that has at least one
// delegated client in mgr's snapshot.
func Delegated(mgr *cxosub.Manager) map[string]struct{} {
	out := map[string]struct{}{}
	if mgr == nil {
		return out
	}
	mgr.Walk(cxosub.FeedDMSGDClientsByServer, Prefix, func(path string, _ []byte) bool {
		if rel := strings.TrimPrefix(path, Prefix); rel != "" && !strings.Contains(rel, "/") {
			out[rel] = struct{}{}
		}
		return true
	})
	return out
}
