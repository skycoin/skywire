// Package cliut cmd/skywire-cli/ut/sd.go
//
// `skywire cli ut sd` — visor uptime as reported by the
// service-discovery integrated endpoint. Populated by the heartbeats
// visors already send to SD when registering services, so it's the
// "cheapest" uptime source: no dedicated heartbeat path, shares the
// SD registration flow.
package cliut

import (
	"fmt"

	"github.com/skycoin/skywire/cmd/skywire-cli/cliuptime"
)

func init() {
	dep := getDeployment()
	utCmd.AddCommand(cliuptime.NewCmd(cliuptime.Config{
		Use:        "sd",
		DefaultURL: dep.ServiceDiscovery,
		Short:      "query service-discovery integrated uptime tracker",
		Long: fmt.Sprintf(`Query the Service-Discovery integrated uptime endpoint.

%s/uptimes

Default is v2 (includes daily percentages). Pass -T / --timeline to
request v3 and render the per-5-minute bitmap as 24 hourly blocks.`, dep.ServiceDiscovery),
	}))
}
