// Package visorapi pkg/visor/visorapi/dmsg.go c3-vis-core
package visorapi

import (
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// DmsgConnectAllResult summarizes the result of DmsgConnectAll.
type DmsgConnectAllResult struct {
	Total            int               `json:"total"`             // total servers in discovery
	AlreadyConnected int               `json:"already_connected"` // sessions in place before the call
	NewlyConnected   int               `json:"newly_connected"`   // sessions opened by the call
	Failed           map[string]string `json:"failed,omitempty"`  // server PK → error text for any that could not be connected
}

// DmsgConvergeResult reports the outcome of a carrier-convergence pass.
type DmsgConvergeResult struct {
	Carriers  []string              `json:"carriers"`  // the effective ordered preference used
	Converged int                   `json:"converged"` // sessions re-dialed to a more-preferred carrier
	Sessions  []dmsg.SessionCarrier `json:"sessions"`  // per-session carrier after the pass
}

// DmsgPorterStatus contains DMSG porter diagnostics.
type DmsgPorterStatus struct {
	MainPorts int `json:"main_ports"`
	RSNPorts  int `json:"rsn_ports,omitempty"`
	MainFreed int `json:"main_freed,omitempty"`
	RSNFreed  int `json:"rsn_freed,omitempty"`
}

// DmsgProbeReasonResponse carries a probe result together with why it failed.
type DmsgProbeReasonResponse struct {
	Reachable bool
	Reason    string
}
