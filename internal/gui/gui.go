//go:build systray
// +build systray

package gui

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
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
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// TODO @alexadhy : Show VPN status, list all vpn servers, quick dial

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
	mOpenHypervisor *systray.MenuItem
	//mVPNClient      *systray.MenuItem
	mUninstall *systray.MenuItem
	mQuit      *systray.MenuItem
)

// GetOnGUIReady creates func to run on GUI startup.
func GetOnGUIReady(icon []byte, conf *visorconfig.V1) func() {
	doneCh := make(chan bool, 1)
	return func() {
		systray.SetTemplateIcon(icon, icon)

		systray.SetTooltip("Skywire")

		initOpenHypervisorBtn(conf)
		//initVpnClientBtn()
		initUninstallBtn()
		initQuitBtn()

		//go updateVPNConnectionStatus(conf, doneCh)

		go handleUserInteraction(conf, doneCh)
	}
}

// OnGUIQuit is executed on GUI exit.
func OnGUIQuit() {
	systray.Quit()
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

func initOpenHypervisorBtn(conf *visorconfig.V1) {
	hvAddr := getHVAddr(conf)

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

//func initVpnClientBtn() {
//	mVPNClient = systray.AddMenuItem("VPN", "VPN Client Connection")
//}

//func handleVpnClientButton(conf *visorconfig.V1) {
//	//mVPNClient.AddSubMenuItem()
//}

func GetAvailPublicVPNServers(conf *visorconfig.V1) []string {
	sdClient := servicedisc.NewClient(log, servicedisc.Config{
		Type:     servicedisc.ServiceTypeVPN,
		PK:       conf.PK,
		SK:       conf.SK,
		DiscAddr: skyenv.DefaultServiceDiscAddr,
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

//func updateVPNConnectionStatus(conf *visorconfig.V1, doneCh <-chan bool) {
//	rpcDialTimeout := time.Second * 5
//	conn, err := net.DialTimeout("tcp", conf.CLIAddr, rpcDialTimeout)
//	if err != nil {
//		log.Fatal("RPC Connection failed: ", err)
//	}
//	rpcClient := visor.NewRPCClient(log, conn, visor.RPCPrefix, 0)
//
//	vpnSumChan := make(chan appserver.ConnectionSummary)
//
//	// polls vpn client summary
//	go func(done <-chan bool) {
//		for {
//			if <-done {
//				break
//			}
//			vpnSummary, err := rpcClient.GetAppConnectionsSummary(skyenv.VPNClientName)
//			if err != nil {
//				vpnClientStatusMu.Lock()
//				vpnClientStatus = false
//				vpnClientStatusMu.Unlock()
//			}
//
//			for _, sum := range vpnSummary {
//				vpnSumChan <- sum
//			}
//		}
//	}(doneCh)
//
//	for {
//		select {
//		case sum := <-vpnSumChan:
//			if sum.IsAlive {
//				vpnClientStatusMu.Lock()
//				vpnClientStatus = true
//				vpnClientStatusMu.Unlock()
//			}
//		case <-doneCh:
//			close(vpnSumChan)
//			break
//		}
//	}
//
//}

func initUninstallBtn() {
	mUninstall = systray.AddMenuItem("Uninstall", "")
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
