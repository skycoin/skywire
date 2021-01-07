package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/getlantern/systray"
	"github.com/skratchdot/open-golang/open"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var log = logging.NewMasterLogger()

func main() {
	systray.Run(onGUIReady, onGUIQuit)
}

func startVisor() {
	// disable the button, so user won't click on it again
	mStartStopVisor.Disable()

	if err := startVisorDaemon(); err != nil {
		log.WithError(err).Errorln("Failed to start visor daemon")
		return
	}

	t := time.NewTicker(2 * time.Second)
	defer t.Stop()

	// wait till the visor service is up
	for {
		<-t.C

		visorRunning, err := isVisorRunning()
		if err != nil {
			log.WithError(err).Fatalln("Failed to get visor state")
		}

		if visorRunning {
			mStartStopVisor.SetTitle(stopVisorTitle)
			mStartStopVisor.Enable()
			break
		}
	}

	vConf, err := readVisorConfig()
	if err != nil {
		log.WithError(err).Errorln("Failed to read visor config")
		return
	}

	hvAddr := getHVAddr(vConf)
	if hvAddr == "" {
		// hypervisor config is not defined
		return
	}

	// wait till the hypervisor is up
	for {
		<-t.C

		if isHypervisorRunning(hvAddr) {
			mOpenHypervisor.Enable()
			break
		}
	}
}

func stopVisor() {
	// disable buttons, so user won't click on them again
	mOpenHypervisor.Disable()
	mStartStopVisor.Disable()

	if err := stopVisorDaemon(); err != nil {
		log.WithError(err).Errorln("Failed to stop visor daemon")
		return
	}

	t := time.NewTicker(3 * time.Second)
	defer t.Stop()

	// wait till visor is down
	for {
		<-t.C

		visorRunning, err := isVisorRunning()
		if err != nil {
			log.WithError(err).Fatalln("Failed to get visor state")
		}

		if !visorRunning {
			mStartStopVisor.SetTitle(startVisorTitle)
			mStartStopVisor.Enable()
			break
		}
	}
}

func isHypervisorRunning(addr string) bool {
	// we check if it's up by querying `health` endpoint
	resp, err := http.Get(addr + "/api/health")
	if err != nil {
		// hypervisor is not running in this case
		return false
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.WithError(err).Errorln("Failed to close hypervisor response body")
		}
	}()

	if _, err := io.Copy(ioutil.Discard, resp.Body); err != nil {
		log.WithError(err).Errorln("Failed to discard hypervisor response body")
	}

	return true
}

func openHypervisor() error {
	conf, err := readVisorConfig()
	if err != nil {
		return fmt.Errorf("failed to read visor config: %w", err)
	}

	hvAddr := getHVAddr(conf)
	if hvAddr == "" {
		return nil
	}

	log.Infoln("Opening hypervisor at %s", hvAddr)

	if err := open.Run(hvAddr); err != nil {
		return fmt.Errorf("failed to open link: %w", err)
	}

	return nil
}

func getHVAddr(conf *visorconfig.V1) string {
	if conf.Hypervisor == nil {
		return ""
	}

	// address may just start with the colon, so we make it valid by
	// adding leading schema and address
	addr := strings.TrimSpace(conf.Hypervisor.HTTPAddr)
	if addr[0] == ':' {
		addr = "http://localhost" + addr
	}

	return addr
}

func readVisorConfig() (*visorconfig.V1, error) {
	confPath := "/opt/skywire/skywire-config.json"
	if runtime.GOOS == "windows" {
		// TODO: set path for windows config
	}

	f, err := os.Open(confPath) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	raw, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("failed to read in config: %w", err)
	}

	if err := f.Close(); err != nil {
		log.WithError(err).Error("Closing config file resulted in error.")
	}

	conf, err := visorconfig.Parse(log, confPath, raw)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	return conf, nil
}
