//go:build !mobile

// Package visor pkg/visor/hypervisor_dmsg_ingest.go c3-vis-core
package visor

import (
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// hypervisorIngestClient returns the dmsg client the hypervisor serves its
// remote-visor RPC ingest on (Hypervisor.Enable only starts ServeRPC when the
// client is non-nil). Desktop builds always hand it over — a configured
// hypervisor manages remote visors. The mobile pair gates it behind
// hypervisor.dmsg_ingest (the Fleet opt-in).
func (v *Visor) hypervisorIngestClient(_ *visorconfig.HypervisorConfig) *dmsg.Client {
	return v.dmsgC
}
