//go:build systray
// +build systray

package gui

import (
	"context"
	"embed"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gen2brain/dlgs"
	"github.com/getlantern/systray"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/toqueteos/webbrowser"

	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// TODO @alexadhy : Show VPN status, list all vpn servers, quick dial

//go:embed icons/*
var iconFS embed.FS

var log = logging.NewMasterLogger()

var (
	stopVisorFnMx sync.Mutex
	stopVisorFn   func()
	//vpnClientStatusMu sync.Mutex
	//vpnClientStatus   bool
)

var (
	guiStopped int32
)

var (
	mAdvancedButton *systray.MenuItem
	mOpenHypervisor *systray.MenuItem
	//mVPNClient      *systray.MenuItem
	mVPNLink   *systray.MenuItem
	mUninstall *systray.MenuItem
	mQuit      *systray.MenuItem
)

// GetOnGUIReady creates func to run on GUI startup.
func GetOnGUIReady(icon []byte, conf *visorconfig.V1, vis *visor.Visor) func() {
	doneCh := make(chan bool, 1)
	return func() {
		systray.SetTemplateIcon(icon, icon)

		systray.SetTooltip("Skywire")

		initOpenVPNLinkBtn(vis)
		initAdvancedButton(conf)
		//initVpnClientBtn()
		initQuitBtn()

		//go updateVPNConnectionStatus(conf, doneCh)

		go handleUserInteraction(conf, doneCh)
	}
}

// OnGUIQuit is executed on GUI exit.
func OnGUIQuit() {
}

// ReadSysTrayIcon reads system tray icon.
func ReadSysTrayIcon() (contents []byte, err error) {
	if runtime.GOOS != "darwin" {
		contents, err = iconFS.ReadFile(iconName)
	} else {
		contents, err = ioutil.ReadFile(iconName)
	}

	if err != nil {
		err = fmt.Errorf("failed to read icon: %w", err)
	}

	return contents, err
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

func initAdvancedButton(conf *visorconfig.V1) {
	hvAddr := getHVAddr(conf)

	mAdvancedButton = systray.AddMenuItem("Advanced", "Advanced Menu")
	mOpenHypervisor = mAdvancedButton.AddSubMenuItem("Open Hypervisor", "Open Hypervisor")
	mUninstall = mAdvancedButton.AddSubMenuItem("Uninstall", "Uninstall Application")

	// if it's not installed via package, hide the uninstall button
	initUninstallBtn()

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

func initOpenVPNLinkBtn(vis *visor.Visor) {
	mVPNLink = systray.AddMenuItem("Open VPN UI", "Open VPN UI in browser")

	mVPNLink.Disable()

	// wait for the vpn client to start in the background
	// if it's not started or if it isn't alive just disable the link.
	go func() {
		t := time.NewTicker(1 * time.Second)
		defer t.Stop()

		// we simply wait till the hypervisor is up
		for {
			<-t.C
			if isVPNExists(vis) {
				mVPNLink.Enable()
				break
			} else {
				mVPNLink.Disable()
			}
		}
	}()
}

//func initVpnClientBtn() {
//	mVPNClient = systray.AddMenuItem("VPN", "VPN Client Connection")
//}

//func handleVpnClientButton(conf *visorconfig.V1) {
//	//mVPNClient.AddSubMenuItem()
//}

func handleVPNLinkButton(conf *visorconfig.V1) {
	vpnAddr := getVPNAddr(conf)

	if vpnAddr == "" {
		mVPNLink.Disable()
		log.Error("empty vpn URL address")
		return // do nothing
	}

	if err := webbrowser.Open(vpnAddr); err != nil {
		log.WithError(err).Error("failed to open link")
	}
}

// GetAvailPublicVPNServers gets all available public VPN server from service discovery URL
func GetAvailPublicVPNServers(conf *visorconfig.V1) []string {
	sdClient := servicedisc.NewClient(log, servicedisc.Config{
		Type:     servicedisc.ServiceTypeVPN,
		PK:       conf.PK,
		SK:       conf.SK,
		DiscAddr: conf.Launcher.ServiceDisc,
	})
	//ctx, _ := context.WithTimeout(context.Background(), 7*time.Second)
	vpnServers, err := sdClient.Services(context.Background(), 0)
	if err != nil {
		log.Error("Error getting public vpn servers: ", err)
		return nil
	}
	serverAddrs := make([]string, len(vpnServers))
	for idx, server := range vpnServers {
		serverAddrs[idx] = server.Addr.PubKey().String()
	}
	return serverAddrs
}

func initUninstallBtn() {
	if !checkIsPackage() {
		mUninstall.Hide()
	}
}

func initQuitBtn() {
	mQuit = systray.AddMenuItem("Quit", "")
}

func handleUserInteraction(conf *visorconfig.V1, doneCh chan<- bool) {
	for {
		select {
		case <-mOpenHypervisor.ClickedCh:
			handleOpenHypervisor(conf)
		//case <-mVPNClient.ClickedCh:
		//	handleVpnClientButton(conf)
		case <-mVPNLink.ClickedCh:
			handleVPNLinkButton(conf)
		case <-mUninstall.ClickedCh:
			handleUninstall()
		case <-mQuit.ClickedCh:
			doneCh <- true
			Stop()
		}
	}
}

func handleOpenHypervisor(conf *visorconfig.V1) {
	if err := openHypervisor(conf); err != nil {
		log.WithError(err).Errorln("Failed to open hypervisor")
	}
}

func handleUninstall() {
	cond, err := dlgs.Question("Uninstall", "Do you want to uninstall visor?", true)
	if err != nil {
		return
	}
	if cond {
		mOpenHypervisor.Disable()
		mVPNLink.Disable()
		mUninstall.Disable()
		mQuit.Disable()

		stopVisor()

		if err := platformExecUninstall(); err != nil {
			mUninstall.Enable()
			log.WithError(err).Errorln("Failed to run deinstaller")
			return
		}
		systray.Quit()
	}
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

func openHypervisor(conf *visorconfig.V1) error {
	hvAddr := getHVAddr(conf)
	if hvAddr == "" {
		return nil
	}

	log.Infof("Opening hypervisor at %s", hvAddr)

	if err := webbrowser.Open(hvAddr); err != nil {
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

func isVPNExists(vis *visor.Visor) bool {
	apps, err := vis.Apps()

	var status bool

	if err != nil {
		status = false
	}

	for _, app := range apps {
		if app.Name == skyenv.VPNClientName {
			status = true
			//if app.Status == launcher.AppStatusRunning {
			//	status = true
			//}
		}
	}

	return status
}

func getVPNAddr(conf *visorconfig.V1) string {
	hvAddr := getHVAddr(conf)
	if hvAddr == "" {
		return ""
	}

	return hvAddr + "/#/vpn/" + conf.PK.Hex() + "/status"
}
