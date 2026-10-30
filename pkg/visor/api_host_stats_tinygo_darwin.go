//go:build tinygo && darwin

package visor

import (
	"errors"

	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// gopsutil reads these through cgo and the macOS SDK headers, which TinyGo
// does not ship, so a TinyGo build for macOS reports no host stats.
func collectLoadStats() *visorapi.LoadStats { return &visorapi.LoadStats{} }

// HostStats implements API.
func (v *Visor) HostStats() (*visorapi.HostStatsInfo, error) {
	return nil, errors.New("host stats are not available in a TinyGo build for macOS")
}
