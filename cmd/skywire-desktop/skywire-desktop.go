package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/visor/visorconfig"

	"github.com/getlantern/systray"
	"github.com/skratchdot/open-golang/open"
)

func main() {
	systray.Run(onReady, onQuit)
}

var log = logging.NewMasterLogger()

func onReady() {
	systray.SetTitle("Skywire")

	mOpenHypervisor := systray.AddMenuItem("Open Hypervisor", "")
	mQuit := systray.AddMenuItem("Quit", "")
	go func() {
		<-mQuit.ClickedCh
		systray.Quit()
	}()

	go func() {

		for {
			<-mOpenHypervisor.ClickedCh

			if err := openHypervisor(); err != nil {
				log.WithError(err).Errorln("Failed to open hypervisor")
			}
		}
	}()
}

func onQuit() {

}

func openHypervisor() error {
	hvAddr, err := getHVAddr()
	if err != nil {
		return fmt.Errorf("failed to get hypervisor address: %w", err)
	}

	log.Infoln("Opening hypervisor at %s", hvAddr)

	if err := open.Run(hvAddr); err != nil {
		return fmt.Errorf("failed to open link: %w", err)
	}

	return nil
}

func getHVAddr() (string, error) {
	f, err := os.Open("/opt/skywire/skywire-config.json") //nolint:gosec
	if err != nil {
		return "", fmt.Errorf("failed to read config file: %w", err)
		log.WithError(err).
			Fatal("Failed to read config file.")
	}

	raw, err := ioutil.ReadAll(f)
	if err != nil {
		return "", fmt.Errorf("failed to read in config: %w", err)
		log.WithError(err).Fatal("Failed to read in config.")
	}

	if err := f.Close(); err != nil {
		log.WithError(err).Error("Closing config file resulted in error.")
	}

	conf, err := visorconfig.Parse(log, "/opt/skywire/skywire-config.json", raw)
	if err != nil {
		return "", fmt.Errorf("failed to parse config: %w", err)
		log.WithError(err).Fatal("Failed to parse config.")
	}

	addr := strings.TrimSpace(conf.Hypervisor.HTTPAddr)
	if addr[0] == ':' {
		addr = "http://localhost" + addr
	}

	return addr, nil
}
