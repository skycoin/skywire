//go:build mobile

// Package visor pkg/visor/hypervisor_dmsg_ingest_mobile.go c3-vis-core
package visor

import (
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// hypervisorIngestClient: on the mobile build the remote-visor dmsg ingest is
// OFF unless the user flips the Fleet opt-in (hypervisor.dmsg_ingest). With a
// nil client Hypervisor.Enable never starts ServeRPC, so the phone core stays
// an API-only localhost surface — no dmsg listener for remote visors, no
// battery cost. The local HTTP API (startUI) is independent of this client.
func (v *Visor) hypervisorIngestClient(conf *visorconfig.HypervisorConfig) *dmsg.Client {
	if conf.DmsgIngest {
		return v.dmsgC
	}
	return nil
}
