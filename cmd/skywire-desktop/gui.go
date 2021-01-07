package main

import (
	"time"

	"github.com/getlantern/systray"
)

var (
	mStartStopVisor *systray.MenuItem
	mOpenHypervisor *systray.MenuItem
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

func initQuitBtn() {
	mQuit := systray.AddMenuItem("Quit", "")
	go func() {
		<-mQuit.ClickedCh
		systray.Quit()
	}()
}

func handleUserInteraction() {
	for {
		select {
		case <-mOpenHypervisor.ClickedCh:
			if err := openHypervisor(); err != nil {
				log.WithError(err).Errorln("Failed to open hypervisor")
			}
		case <-mStartStopVisor.ClickedCh:
			visorRunning, err := isVisorRunning()
			if err != nil {
				log.WithError(err).Errorln("Failed to get visor state")
				continue
			}

			if visorRunning {
				stopVisor()
			} else {
				startVisor()
			}
		}
	}
}
