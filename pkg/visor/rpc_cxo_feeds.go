// Package visor pkg/visor/rpc_cxo_feeds.go
//
// RPC adapter for the CXO user-feed registry. The handlers are thin
// wrappers over Visor.RegisterCXOFeed / UnregisterCXOFeed /
// ListCXOFeeds; per-feed lifecycle management lives in
// cxo_user_feeds.go.
package visor

import (
	"fmt"

	"github.com/skycoin/skywire/pkg/util/rpcutil"
	"github.com/skycoin/skywire/pkg/visor/logserver"
)

// RegisterCXOFeedRequest is the input to RPC.RegisterCXOFeed.
type RegisterCXOFeedRequest struct {
	Name        string `json:"name"`
	DmsgPort    uint16 `json:"dmsg_port"`
	Description string `json:"description,omitempty"`
}

// RegisterCXOFeed creates a new user-published CXO TreeStore feed
// listening on dmsgPort under the visor's PK. The feed is reachable
// from any peer that subscribes to (visor PK, dmsgPort) over DMSG.
func (r *RPC) RegisterCXOFeed(req *RegisterCXOFeedRequest, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "RegisterCXOFeed", req)(nil, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	return r.visor.RegisterCXOFeed(req.Name, req.DmsgPort, req.Description)
}

// UnregisterCXOFeed stops the named user feed (no-op for unknown
// names). The reserved system feed name "stats" cannot be removed.
func (r *RPC) UnregisterCXOFeed(name *string, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "UnregisterCXOFeed", name)(nil, &err)
	if name == nil {
		return fmt.Errorf("nil request")
	}
	return r.visor.UnregisterCXOFeed(*name)
}

// ListCXOFeeds returns the current set of feeds (system + user).
// Mirrors what the dmsghttp logserver exposes at /feeds for
// out-of-band discovery.
func (r *RPC) ListCXOFeeds(_ *struct{}, out *[]logserver.CXOFeedEntry) (err error) {
	defer rpcutil.LogCall(r.log, "ListCXOFeeds", nil)(out, &err)
	*out = r.visor.ListCXOFeeds()
	return nil
}
