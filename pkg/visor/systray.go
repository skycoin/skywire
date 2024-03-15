//go:build !withoutsystray
// +build !withoutsystray

// Package visor pkg/visor/systray.go
package visor

import (
	"context"

	"github.com/skycoin/systray"
)

func runAppSystray() {
	sysTrayIcon, err := readSysTrayIcon()
	if err != nil {
		mLog.WithError(err).Fatalln("Failed to read system tray icon")
	}

	conf := initConfig()

	go func() {
		err := run(conf)
		if err != nil {
			mLog.WithError(err).Fatal("a fatal error occurred")
		}
		systray.Quit()
	}()

	systray.Run(getOnGUIReady(sysTrayIcon, conf), onGUIQuit)

}

func setStopFunctionSystray(cancel context.CancelFunc, fn func() error) { //nolint:unused
	stopVisorWg.Add(1)
	defer stopVisorWg.Done()

	stopVisorFn = func() {
		if err := fn(); err != nil {
			mLog.WithError(err).Error("Visor closed with error.")
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
	err := run(nil)
	if err != nil {
		mLog.WithError(err).Fatal("a fatal error occurred")
	}

}

// setStopFunction sets the stop function
func setStopFunction(cancel context.CancelFunc, fn func() error) { //nolint:unused
	stopVisorWg.Add(1)
	defer stopVisorWg.Done()

	stopVisorFn = func() {
		if err := fn(); err != nil {
			mLog.WithError(err).Error("Visor closed with error.")
		}
		cancel()
		stopVisorWg.Wait()
	}
}
