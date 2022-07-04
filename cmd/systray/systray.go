package main

import (
	"embed"
	"fmt"
	"time"

	"github.com/bitfield/script"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/skycoin/systray"
	"github.com/toqueteos/webbrowser"
)

const (
	iconName = "icons/icon.png"
)

//go:embed icons/*
var iconFS embed.FS

func main() {
	onExit := func() {
		now := time.Now()
		fmt.Println("Exit at", now.String())
	}

	systray.Run(onReady, onExit)
}

func onReady() {
	l := logging.NewMasterLogger()
	sysTrayIcon, err := iconFS.ReadFile(iconName)
	if err != nil {
		err = fmt.Errorf("failed to read icon: %w", err)
		l.WithError(err).Fatalln("Failed to read system tray icon")
	}

	systray.SetTemplateIcon(sysTrayIcon, sysTrayIcon)
	systray.SetTitle("skywire")
	systray.SetTooltip("")
	mQuit := systray.AddMenuItem("Quit", "Quit the whole app")
	go func() {
		<-mQuit.ClickedCh
		fmt.Println("Requesting quit")
		systray.Quit()
		fmt.Println("Finished quitting")
	}()

	go func() {
		systray.SetTemplateIcon(sysTrayIcon, sysTrayIcon)
		systray.SetTitle("Skywire")
		systray.SetTooltip("Skywire")
		skywireStatus, err := script.Exec(`skywire-cli visor pk`).Match("FATAL").String()
		if err != nil {
			l.Infof("skywire is not running")
		}
		mAutoconfig := systray.AddMenuItem("skyire-autoconfig", "skywire-autoconfig")
		mHypervisor := systray.AddMenuItem("hypervisor", "hypervisor")
		mVpnUI := systray.AddMenuItem("VPN interface", "VPN interface")
		if (skywireStatus != "") && (skywireStatus != "\n") {
			mHypervisor.Hide()
		}
		systray.AddMenuItem("", "")

		for {
			select {
			case <-mAutoconfig.ClickedCh:
				l.Infof("Running skywire-autoconfig...")
				_, err := script.Exec(`exo-open --launch TerminalEmulator 'sudo skywire-autoconfig'`).Stdout()
				if err != nil {
					l.Errorf("failed to open VPN UI")
				}
				mHypervisor.Show()
			case <-mHypervisor.ClickedCh:
				hvAddr := "http://127.0.0.1:8000"
				l.Infof("Opening hypervisor at %s", hvAddr)
				webbrowser.Open(hvAddr) // nolint
			case <-mVpnUI.ClickedCh:
				_, err := script.Exec(`skywire-cli visor vpn ui`).Stdout()
				if err != nil {
					l.Errorf("failed to open VPN UI")
				}
			case <-mQuit.ClickedCh:
				systray.Quit()
				fmt.Println("Quit2 now...")
				return
			}
		}
	}()
}
