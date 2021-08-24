//+build systray

package commands

import (
	"context"
	"sync"

	"github.com/getlantern/systray"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/internal/gui"
)

var (
	stopVisorWg   sync.WaitGroup
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
	}()

	conf := initConfig(l, args, confPath)

	systray.Run(gui.GetOnGUIReady(sysTrayIcon, conf), gui.OnGUIQuit)

}

func stopVisor(log *logging.MasterLogger, cancel context.CancelFunc, stopVisorFn func() error) {
	gui.SetStopVisorFn(func() {
		if err := stopVisorFn(); err != nil {
			log.WithError(err).Error("Visor closed with error.")
		}
	})

	gui.Stop()
}

func quitSystray() {
	systray.Quit()
}
