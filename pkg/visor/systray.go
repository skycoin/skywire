//go:build !withoutsystray
// +build !withoutsystray

// Package visor pkg/visor/systray.go
package visor

import (
	"fyne.io/systray"
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

func runApp() {
	err := run(nil)
	if err != nil {
		mLog.WithError(err).Fatal("a fatal error occurred")
	}

}
