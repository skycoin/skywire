// Package visor pkg/visor/flightrec.go c3-vis-core
package visor

import (
	"errors"
	"path/filepath"
	"runtime"

	"github.com/skycoin/skywire/pkg/flightrec"
)

// startFlightRecorder starts the execution trace flight recorder
// (pkg/flightrec) when the config turns it on. It is off by default
// because it allocates heavily, and `skywire cli config set
// flight_recorder=true` starts it on a running visor. Not in a browser
// tab, where the visor shares one thread with everything else on the page.
func (v *Visor) startFlightRecorder() {
	if v.conf == nil || !v.conf.FlightRecorder {
		return
	}
	if err := v.runFlightRecorder(true); err != nil {
		v.MasterLogger().PackageLogger("flightrec").WithError(err).Warn("Flight recorder not started.")
	}
}

// runFlightRecorder starts or stops the recorder.
func (v *Visor) runFlightRecorder(on bool) error {
	if !on {
		flightrec.Stop()
		return nil
	}
	if runtime.GOOS == "js" || v.conf == nil || v.conf.LocalPath == "" {
		return errors.New("the flight recorder needs a native visor with a local path")
	}
	log := v.MasterLogger().PackageLogger("flightrec")
	return flightrec.Start(filepath.Join(v.conf.LocalPath, "log", "flightrec"), log.Infof)
}
