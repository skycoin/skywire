// Package app pkg/app/dial_errors.go c2-vis-appsvc
package app

import (
	"errors"
	"strings"

	"github.com/skycoin/skywire/pkg/router"
)

// IsNoDisjointFirstHop reports whether a failed app dial failed because the
// router had no disjoint first hop left to offer — the settled answer a
// RequireDisjointFirstHop dial gets once every route to the destination leaves
// over a first hop a sibling route group already holds.
//
// A caller growing a pool of sibling tunnels uses it to tell "the topology has
// another disjoint route" from "it has none left", and must STOP on true. It
// is not a transient failure and re-dialing against it is the setup-node storm
// of #4325.
//
// The substring check is not belt-and-braces, it is the load-bearing half: an
// app dial crosses a net/rpc boundary to the visor, which flattens every error
// to its text (appserver.RPCErr), so errors.Is alone can only ever match
// in-process callers.
func IsNoDisjointFirstHop(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, router.ErrNoDisjointFirstHop) {
		return true
	}
	return strings.Contains(err.Error(), router.ErrNoDisjointFirstHop.Error())
}
