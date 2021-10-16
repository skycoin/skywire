//go:build systray
// +build systray

package commands

import (
	"context"

	"github.com/getlantern/systray"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/internal/gui"
)

var (
	runSysTrayApp bool
)

func extraFlags() {
	rootCmd.Flags().BoolVar(&runSysTrayApp, "systray", false, "Run system tray app")
}

func runApp(args ...string) {
	l := logging.NewMasterLogger()
	sysTrayIcon, err := gui.ReadSysTrayIcon()
	if err != nil {
		l.WithError(err).Fatalln("Failed to read system tray icon")
	}

	go func() {
		runVisor(args)
		systray.Quit()
	}()

	conf := initConfig(l, args, confPath)

	systray.Run(gui.GetOnGUIReady(sysTrayIcon, conf), gui.OnGUIQuit)

}

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

	gui.SetStopVisorFn(func() {
		stopVisorFn()
	})
}

func quitSystray() {
	systray.Quit()
}
