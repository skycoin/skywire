//go:build !withoutsystray
// +build !withoutsystray

// Package visor pkg/visor/systray.go
package visor

import (
	"context"

	"fyne.io/systray"
)

func runAppSystray() {
	sysTrayIcon, err := readSysTrayIcon()
	if err != nil {
		mLog.WithError(err).Fatalln("Failed to read system tray icon")
	}

	conf := initConfig()

	go func() {
		err := run(context.Background(), conf)
		if err != nil {
			mLog.WithError(err).Fatal("a fatal error occurred")
		}
		systray.Quit()
	}()

	systray.Run(getOnGUIReady(sysTrayIcon, conf), onGUIQuit)

}

func runApp() {
	err := run(context.Background(), nil)
	if err != nil {
		mLog.WithError(err).Fatal("a fatal error occurred")
	}

}
