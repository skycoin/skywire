// Package cliut cmd/skywire-cli/ut/mdisc.go
//
// `skywire cli ut mdisc` — visor uptime as reported by the
// dmsg-discovery integrated endpoint. Uptime data is derived from
// dmsg session activity, which makes it the most accurate "is this
// visor actually reachable via dmsg?" source.
package cliut

import (
	"fmt"

	"github.com/skycoin/skywire/cmd/skywire-cli/cliuptime"
)

func init() {
	dep := getDeployment()
	utCmd.AddCommand(cliuptime.NewCmd(cliuptime.Config{
		Use:        "mdisc",
		DefaultURL: dep.DmsgDiscovery,
		Short:      "query DMSG-discovery integrated uptime tracker",
		Long: fmt.Sprintf(`Query the DMSG-Discovery integrated uptime endpoint.

%s/uptimes

Default is v2 (includes daily percentages). Pass -T / --timeline to
request v3 and render the per-5-minute bitmap as 24 hourly blocks.`, dep.DmsgDiscovery),
	}))
}
