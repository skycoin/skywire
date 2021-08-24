//+build !systray

package commands

import (
	"context"
)

func extraFlags() {

}

func runApp(args ...string) {
	runVisor(args)
}

// stopSystray is a stub
func stopSystray(ctx context.CancelFunc, stopVisorFn func() error) {
	if err := stopVisorFn(); err != nil {
		log.WithError(err).Error("Visor closed with error.")
	}
}

// quitSystray is a stub
func quitSystray() {
}
