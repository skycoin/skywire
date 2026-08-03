//go:build !withoutsystray
// +build !withoutsystray

// Package visor pkg/visor/systray.go c3-vis-core
package visor

import (
	"context"

	"fyne.io/systray"

	"github.com/skycoin/skywire/pkg/visor/visorconfig"
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

// runTrayOnly runs ONLY the system tray — it does NOT start a visor in-process.
// The tray's controls already talk to the visor purely over its local RPC
// (rpcClientSystray dials conf.CLIAddr), so decoupling them lets the visor run
// as an always-on background service (systemd/launchd/Windows service) while the
// tray is an unprivileged desktop app launched in the user's session. Unlike
// runAppSystray, there is no in-process `run(ctx, conf)` — the tray simply
// connects to whichever visor is already serving on cli_addr, retrying until it
// comes up. Selected by `skywire visor --systray-only`.
func runTrayOnly() {
	sysTrayIcon, err := readSysTrayIcon()
	if err != nil {
		mLog.WithError(err).Fatalln("Failed to read system tray icon")
	}

	// Build a minimal config from deployment defaults + the --rpc address rather
	// than reading the visor's config file. That file holds the visor's secret key
	// and should be root-only (0640) — the unprivileged tray must not depend on it.
	// The tray talks to the visor purely over the local RPC at CLIAddr and never
	// needs the visor identity. The Hypervisor-UI link defaults to the conventional
	// web address and is enabled only after a live probe (initUIBtns / getHVAddr),
	// so a wrong guess just leaves the link hidden instead of opening a dead page.
	conf := visorconfig.MakeBaseConfig(nil, false, false, nil, nil)
	conf.CLIAddr = systrayRPCAddr
	conf.Hypervisor = &visorconfig.HypervisorConfig{HTTPAddr: ":8000"}

	systray.Run(getOnGUIReady(sysTrayIcon, conf), onGUIQuit)
}
