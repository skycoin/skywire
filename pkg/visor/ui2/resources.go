// Package ui2 pkg/visor/ui2/resources.go c3-vis-core
package ui2

import (
	"strconv"

	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// The host resources of the visors, as multi-visor-resources.component.ts
// and resource-monitor.component.ts show them.

// ResRow is one visor's host stats.
type ResRow struct {
	Node
	Stats *visorapi.HostStatsInfo
	Err   string
	// TxRate and RxRate are bytes per second since the previous sample,
	// negative before there is one.
	TxRate float64
	RxRate float64
}

// VisorRes is one visor's resources tab.
type VisorRes struct {
	PK      string
	Host    *visorapi.HostStatsInfo
	Runtime *visorapi.RuntimeStatsInfo
	HostErr string
	RunErr  string
	TxRate  float64
	RxRate  float64
	Sampled string
}

// pctTone follows the Angular thresholds: 90 and over is bad, 70 a warning.
func pctTone(p float64) string {
	switch {
	case p >= 90:
		return "error"
	case p >= 70:
		return "warn-text"
	}
	return "ok"
}

// Rate prints bytes per second, or "-" when unknown.
func Rate(bps float64) string {
	if bps < 0 {
		return "-"
	}
	return Bytes(uint64(bps)) + "/s"
}

// HostUptime prints seconds as days, hours and minutes.
func HostUptime(s uint64) string {
	days, h, m := s/86400, s/3600%24, s/60%60
	if days > 0 {
		return strconv.FormatUint(days, 10) + "d " + strconv.FormatUint(h, 10) + "h"
	}
	return strconv.FormatUint(h, 10) + "h " + strconv.FormatUint(m, 10) + "m"
}
