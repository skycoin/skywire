//go:build !systray
// +build !systray

package commands

import (
	"context"

	"github.com/skycoin/skycoin/src/util/logging"
)

func extraFlags() {

}

func runApp(args ...string) {
	runVisor(args)
}

// setStopFunction sets the stop function
func setStopFunction(log *logging.MasterLogger, cancel context.CancelFunc, fn func() error) {
	stopVisorWg.Add(1)
	defer stopVisorWg.Done()

	stopVisorFn = func() {
		if err := fn(); err != nil {
			log.WithError(err).Error("Visor closed with error.")
		}
		cancel()
		stopVisorWg.Wait()
	}
}

// quitSystray is a stub
func quitSystray() {
}
