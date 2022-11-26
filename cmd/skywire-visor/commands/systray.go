// Package commands cmd/skywire-visor/commands/systray.go
package commands

import (
	"context"

	"github.com/skycoin/systray"

	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/internal/gui"
	"github.com/skycoin/skywire/pkg/skyenv"
)

func runAppSystray() {
	l := logging.NewMasterLogger()
	sysTrayIcon, err := gui.ReadSysTrayIcon()
	if err != nil {
		l.WithError(err).Fatalln("Failed to read system tray icon")
	}

	conf := initConfig(l, confPath)

	go func() {
		runVisor(conf)
		systray.Quit()
	}()

	systray.Run(gui.GetOnGUIReady(sysTrayIcon, conf), gui.OnGUIQuit)

}

func setStopFunctionSystray(log *logging.MasterLogger, cancel context.CancelFunc, fn func() error) { //nolint:unused
	skyenv.StopVisorWg.Add(1)
	defer skyenv.StopVisorWg.Done()

	skyenv.StopVisorFn = func() {
		if err := fn(); err != nil {
			log.WithError(err).Error("Visor closed with error.")
		}
		cancel()
		skyenv.StopVisorWg.Wait()
	}

	gui.SetStopVisorFn(func() {
		skyenv.StopVisorFn()
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
	skyenv.StopVisorWg.Add(1)
	defer skyenv.StopVisorWg.Done()

	skyenv.StopVisorFn = func() {
		if err := fn(); err != nil {
			log.WithError(err).Error("Visor closed with error.")
		}
		cancel()
		skyenv.StopVisorWg.Wait()
	}
}
