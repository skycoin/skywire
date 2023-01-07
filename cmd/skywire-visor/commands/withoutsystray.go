//go:build withoutsystray
// +build withoutsystray

// Package commands cmd/skywire-visor/commands/systray.go
package commands

import (
	"context"

	"github.com/skycoin/skywire-utilities/pkg/logging"
)

func runAppSystray() {
	runVisor(nil)
}

func setStopFunctionSystray(log *logging.MasterLogger, cancel context.CancelFunc, fn func() error) {
	setStopFunction(log, cancel, fn)
}

func quitSystray() {
	quit()
}

func runApp() {
	runVisor(nil)
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

func quit() {

}
