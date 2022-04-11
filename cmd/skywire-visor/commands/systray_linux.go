//go:build systray && linux
// +build systray,linux

package commands

import (
	"context"

	"github.com/getlantern/systray"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/internal/gui"
)

func runApp(args ...string) {
	//systray app cannot launch browser as root
	if root {
		runVisor(nil)
	} else {
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

	//systray app cannot launch browser as root
	if root {
		gui.SetStopVisorFn(func() {
			stopVisorFn()
		})
	}

}

func quitSystray() {
	if root {
		systray.Quit()
	}
}
