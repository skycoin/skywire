// Package visor pkg/visor/flightrec.go c3-vis-core
package visor

import (
	"path/filepath"
	"runtime"

	"github.com/skycoin/skywire/pkg/flightrec"
)

// startFlightRecorder keeps the last seconds of execution trace in memory
// (pkg/flightrec), so a stall detector can save it and an operator can fetch
// it with `skywire cli log pprof <pk> flightrecorder`. Not in a browser tab,
// where the visor shares one thread with everything else on the page.
func (v *Visor) startFlightRecorder() {
	if runtime.GOOS == "js" || v.conf == nil || v.conf.LocalPath == "" {
		return
	}
	log := v.MasterLogger().PackageLogger("flightrec")
	if err := flightrec.Start(filepath.Join(v.conf.LocalPath, "log", "flightrec"), log.Infof); err != nil {
		log.WithError(err).Warn("Flight recorder not started.")
	}
}
