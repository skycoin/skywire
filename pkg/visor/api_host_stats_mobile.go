//go:build mobile

// Package visor pkg/visor/api_host_stats_mobile.go c3-vis-core
//
// The mobile build has no host stats. The desktop collectors
// (api_host_stats.go) are gopsutil, whose darwin side is cgo against
// libproc.h and IOKit — headers the iOS SDK does not ship — and the phone
// app never asks for the Resource Monitor. Tagging that file out is what
// keeps gopsutil out of the iOS import graph.
package visor

import "github.com/skycoin/skywire/pkg/visor/visorapi"

// collectLoadStats reports no load snapshot on the mobile build. Summary's
// Load is omitempty, so the field is absent rather than a row of zeros that a
// remote hypervisor's fleet view would show as an idle, empty-disk host.
func collectLoadStats() *visorapi.LoadStats {
	return nil
}

// HostStats implements API. The /host-stats handler answers 501 for this
// error (hypervisor_handlers_admin.go).
func (v *Visor) HostStats() (*visorapi.HostStatsInfo, error) {
	return nil, errHostStatsUnavailable
}
