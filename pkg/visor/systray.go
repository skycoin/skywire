// Package visor pkg/visor/systray.go
package visor

import (
	"context"

	"github.com/skycoin/systray"

	"github.com/skycoin/skywire-utilities/pkg/logging"
)

func runAppSystray() {
	l := logging.NewMasterLogger()
	sysTrayIcon, err := readSysTrayIcon()
	if err != nil {
		l.WithError(err).Fatalln("Failed to read system tray icon")
	}

	conf := initConfig(l, confPath)

	go func() {
		runVisor(conf)
		systray.Quit()
	}()

	systray.Run(getOnGUIReady(sysTrayIcon, conf), onGUIQuit)

}

func setStopFunctionSystray(log *logging.MasterLogger, cancel context.CancelFunc, fn func() error) { //nolint:unused
	stopVisorWg.Add(1)
	defer stopVisorWg.Done()

	stopVisorFn = func() {
		if err := fn(); err != nil {
			log.WithError(err).Error("Visor closed with error.")
		}
		cancel()
		stopVisorWg.Wait()
	}

	SetStopVisorFn(func() {
		stopVisorFn()
	})
}

func quitSystray() { //nolint:unused
	systray.Quit()
}

func runApp() {
	runVisor(nil)
}

// setStopFunction sets the stop function
func setStopFunction(log *logging.MasterLogger, cancel context.CancelFunc, fn func() error) { //nolint:unused
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
