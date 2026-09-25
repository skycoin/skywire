// Package visor pkg/visor/options.go c3-vis-core
package visor

import (
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// Options are the settings a visor process starts with that are not part of
// its config file: command-line overrides and where logs go. The zero value
// runs the visor exactly as its config says.
type Options struct {
	// LoadConfig reads the config again for Visor.Reload. When nil the visor
	// cannot reload, because it was handed a config it has no source for.
	LoadConfig func() (*visorconfig.V1, error)

	// LogLevel overrides the config's log level when set.
	LogLevel string
	// PprofMode is one of cpu, mem, mutex, block, trace or http; empty is off.
	PprofMode string
	// PprofAddr is where the http pprof mode listens.
	PprofAddr string

	// Hypervisors are public keys added to the config's remote hypervisors.
	Hypervisors []string
	// NoHypervisors drops the config's remote hypervisors.
	NoHypervisors bool
	// LaunchBrowser opens the hypervisor UI once the visor is up.
	LaunchBrowser bool
	// NoCSRF stops the hypervisor UI from requiring a CSRF token on
	// requests that change state.
	NoCSRF bool

	// DmsgServer pins the visor to one dmsg server public key.
	DmsgServer string
	// DmsgServerAddr dials DmsgServer at this host:port instead of asking
	// discovery for it.
	DmsgServerAddr string
	// DmsgServerMaxAttempts is how many failed connects to the pinned server
	// end startup. Zero means 5.
	DmsgServerMaxAttempts int

	// StoreLog also writes the log to <local_path>/log/skywire.log.
	StoreLog bool
	// LogJSON also writes structured JSON lines next to it (needs StoreLog).
	LogJSON bool
	// ForceColor colors the log even when it is not a terminal.
	ForceColor bool
}
