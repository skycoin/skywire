// Package cliut cmd/skywire-cli/ut/tpd.go
//
// `skywire cli ut tpd` — visor uptime from the transport-discovery
// integrated endpoint. Earlier this command used a hand-rolled JQ
// expression that silently returned nothing when the server response
// shape changed (it fetched v1 but then tried to filter by the v2
// `daily` field, which doesn't exist at v1). Replaced with the shared
// cliuptime factory so it behaves identically to the sd / mdisc
// equivalents and fetches v2 by default.
package cliut

import (
	"fmt"

	"github.com/skycoin/skywire/cmd/skywire-cli/cliuptime"
)

func init() {
	dep := getDeployment()
	utCmd.AddCommand(cliuptime.NewCmd(cliuptime.Config{
		Use:        "tpd",
		DefaultURL: dep.TransportDiscovery,
		Short:      "query TPD integrated uptime tracker",
		Long: fmt.Sprintf(`Query the Transport-Discovery integrated uptime endpoint.

%s/uptimes

Same response shape as sd / mdisc / ut — v1 (pk+on), v2 (+ daily %%),
v3 (+ per-5-minute timeline bitmap). Default is v2; pass -T / --timeline
to request v3 and render the bitmap as 24 hourly blocks.

Populated by visor heartbeats + transport registrations; the same
data feeds into the rewards pipeline.`, dep.TransportDiscovery),
	}))
}
