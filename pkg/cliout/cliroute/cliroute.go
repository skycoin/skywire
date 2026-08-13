// Package cliroute is the output shape of the `skywire cli route` commands.
//
// It lives here rather than in a function body so callers, and the e2e suite,
// can import it. The suite already runs `route --json`, `route rule --json`
// and `route add-rule ... --json` and parses what comes back.
package cliroute

import (
	"time"

	"github.com/skycoin/skywire/pkg/routing"
)

// Rule is one routing rule as `skywire cli route` reports it.
//
// The optional fields are per rule TYPE, not per flag: an app rule has ports
// and a remote key, a forward rule has a next hop, and neither carries the
// other's fields. omitempty is what keeps one type serving all three without a
// consumer having to recognise three documents.
type Rule struct {
	ID   routing.RouteID `json:"id"`
	Type string          `json:"type"`

	// App rules.
	LocalPort  string `json:"local_port,omitempty"`
	RemotePort string `json:"remote_port,omitempty"`
	RemotePK   string `json:"remote_pk,omitempty"`

	// Forward and intermediary-forward rules.
	NextRouteID string `json:"next_route_id,omitempty"`
	NextTpID    string `json:"next_transport_id,omitempty"`

	// ExpireAt keeps its hyphen: it is the name already emitted, and renaming
	// it would break every consumer parsing it today for the sake of tidiness.
	ExpireAt time.Duration `json:"expire-at"`
}
