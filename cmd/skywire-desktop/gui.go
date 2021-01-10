package main

import (
	"syscall"
	"time"

	"github.com/skycoin/skywire/pkg/util/osutil"

	"github.com/getlantern/systray"
)

var (
	mStartStopVisor *systray.MenuItem
	mOpenHypervisor *systray.MenuItem
	mUninstall      *systray.MenuItem
	mQuit           *systray.MenuItem
)

const (
	startVisorTitle = "Start Visor"
	stopVisorTitle  = "Stop Visor"
)

func onGUIReady() {
	systray.SetTitle("Skywire")

	visorRunning, err := isVisorRunning()
	if err != nil {
		log.WithError(err).Fatalln("Failed to get visor state")
	}

	initStartStopVisorBtn(visorRunning)
	initOpenHypervisorBtn(visorRunning)
	initUninstallBtn()
	initQuitBtn()

	go handleUserInteraction()
}

func onGUIQuit() {

}

func initStartStopVisorBtn(visorRunning bool) {
	mStartStopVisor = systray.AddMenuItem(startVisorTitle, "")
	if visorRunning {
		mStartStopVisor.SetTitle(stopVisorTitle)
	}
}

func initOpenHypervisorBtn(visorRunning bool) {
	vConf, err := readVisorConfig()
	if err != nil {
		log.WithError(err).Fatalln("Failed to read visor config")
	}

	hvAddr := getHVAddr(vConf)

	mOpenHypervisor = systray.AddMenuItem("Open Hypervisor", "")

	// if visor's not running or hypervisor config is absent,
	// there won't be any way to open the hypervisor, so disable button
	if !visorRunning || hvAddr == "" {
		mOpenHypervisor.Disable()
		return
	}

	// if hypervisor is already running, just leave the button enabled
	// as a default
	if isHypervisorRunning(hvAddr) {
		return
	}

	// visor is running, but hypervisor is not yet, so disable the button
	mOpenHypervisor.Disable()

	t := time.NewTicker(1 * time.Second)
	defer t.Stop()

	// we simply wait till the hypervisor is up
	for {
		<-t.C

		if isHypervisorRunning(hvAddr) {
			mOpenHypervisor.Enable()
			break
		}
	}
}

func initUninstallBtn() {
	mUninstall = systray.AddMenuItem("Uninstall", "")
}

func initQuitBtn() {
	mQuit = systray.AddMenuItem("Quit", "")
}

func handleUserInteraction() {
	for {
		select {
		case <-mOpenHypervisor.ClickedCh:
			handleOpenHypervisor()
		case <-mStartStopVisor.ClickedCh:
			handleStartStopVisor()
		case <-mUninstall.ClickedCh:
			handleUninstall()
		case <-mQuit.ClickedCh:
			systray.Quit()
		}
	}
}

func handleOpenHypervisor() {
	if err := openHypervisor(); err != nil {
		log.WithError(err).Errorln("Failed to open hypervisor")
	}
}

func handleStartStopVisor() {
	visorRunning, err := isVisorRunning()
	if err != nil {
		log.WithError(err).Errorln("Failed to get visor state")
		return
	}

	if visorRunning {
		stopVisor()
	} else {
		startVisor()
	}
}

func handleUninstall() {
	mStartStopVisor.Disable()
	mOpenHypervisor.Disable()
	mUninstall.Disable()
	mQuit.Disable()

	/*if err := uninstall(); err != nil {
		mUninstall.Enable()
		log.WithError(err).Errorln("failed to uninstall skywire apps")
		return
	}*/

	suid := syscall.Getuid()

	if err := syscall.Setuid(0); err != nil {
		mUninstall.Enable()
		log.WithError(err).Errorln("failed to setuid 0")
	}

	cmd := "installer -pkg /Users/darkrengarius/go/src/github.com/SkycoinPro/skywire-services/scripts/mac_installer/remover.pkg -target /"
	if err := osutil.Run("/bin/bash", "-c", cmd); err != nil {
		mUninstall.Enable()
		log.WithError(err).Errorln("failed to remove systray app")
		if err := syscall.Setuid(suid); err != nil {
			log.WithError(err).Errorln("Failed to revert uid")
		}
		return
	}

	if err := syscall.Setuid(suid); err != nil {
		log.WithError(err).Errorln("Failed to revert uid")
	}

	return

	cmd = "rm -rf /Applications/Skywire.app"
	if err := osutil.Run("/bin/bash", "-c", cmd); err != nil {
		mUninstall.Enable()
		log.WithError(err).Errorln("failed to remove systray app")
		return
	}

	systray.Quit()
}
