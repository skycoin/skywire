package gui

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/toqueteos/webbrowser"

	"github.com/skycoin/skywire/pkg/skyenv"

	"github.com/getlantern/systray"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/util/osutil"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var log = logging.NewMasterLogger()

var (
	stopVisorFnMx sync.Mutex
	stopVisorFn   func()
)

var (
	guiStopped int32
)

var (
	mOpenHypervisor *systray.MenuItem
	mUninstall      *systray.MenuItem
	mQuit           *systray.MenuItem
)

// GetOnGUIReady creates func to run on GUI startup.
func GetOnGUIReady(icon []byte) func() {
	return func() {
		systray.SetTooltip("Skywire")

		systray.SetTemplateIcon(icon, icon)

		initOpenHypervisorBtn()
		initUninstallBtn()
		initQuitBtn()

		go handleUserInteraction()
	}
}

// OnGUIQuit is executed on GUI exit.
func OnGUIQuit() {

}

// ReadSysTrayIcon reads system tray icon.
func ReadSysTrayIcon() ([]byte, error) {
	contents, err := ioutil.ReadFile(iconPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read icon: %w", err)
	}

	return contents, nil
}

// SetStopVisorFn sets function to stop running visor.
func SetStopVisorFn(fn func()) {
	stopVisorFnMx.Lock()
	stopVisorFn = fn
	stopVisorFnMx.Unlock()
}

// Stop stops visor and quits GUI app.
func Stop() {
	if !atomic.CompareAndSwapInt32(&guiStopped, 0, 1) {
		return
	}

	stopVisor()
	systray.Quit()
}

func initOpenHypervisorBtn() {
	vConf, err := readVisorConfig()
	if err != nil {
		log.WithError(err).Fatalln("Failed to read visor config")
	}

	hvAddr := getHVAddr(vConf)

	mOpenHypervisor = systray.AddMenuItem("Open Hypervisor", "")

	// if visor's not running or hypervisor config is absent,
	// there won't be any way to open the hypervisor, so disable button
	if hvAddr == "" {
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

	// wait for the hypervisor to start in the background
	go func() {
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
	}()
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
		case <-mUninstall.ClickedCh:
			handleUninstall()
		case <-mQuit.ClickedCh:
			Stop()
		}
	}
}

func handleOpenHypervisor() {
	if err := openHypervisor(); err != nil {
		log.WithError(err).Errorln("Failed to open hypervisor")
	}
}

func handleUninstall() {
	mOpenHypervisor.Disable()
	mUninstall.Disable()
	mQuit.Disable()

	stopVisor()

	const deinstallerPath = "/Applications/Skywire.app/Contents/deinstaller"
	if err := osutil.Run("/bin/bash", "-c", deinstallerPath); err != nil {
		mUninstall.Enable()
		log.WithError(err).Errorln("Failed to run deinstaller")
		return
	}

	systray.Quit()
}

func stopVisor() {
	stopVisorFnMx.Lock()
	stop := stopVisorFn
	stopVisorFnMx.Unlock()

	if stop != nil {
		stop()
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

	log.Infof("Opening hypervisor at %s", hvAddr)

	if err := webbrowser.Open("http://golang.org"); err != nil {
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
	confPath := skyenv.PackageSkywirePath() + "/skywire-config.json"

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
