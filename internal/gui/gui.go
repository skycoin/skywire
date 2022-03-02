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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gen2brain/dlgs"
	"github.com/getlantern/systray"
	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/dmsg/direct"
	"github.com/skycoin/dmsg/dmsgget"
	"github.com/skycoin/dmsg/dmsghttp"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/toqueteos/webbrowser"

	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
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
	mVPNClient      *systray.MenuItem
	mVPNLink        *systray.MenuItem
	mVPNButton      *systray.MenuItem
	mUninstall      *systray.MenuItem
	mQuit           *systray.MenuItem
)

// GetOnGUIReady creates func to run on GUI startup.
func GetOnGUIReady(icon []byte, conf *visorconfig.V1) func() {
	doneCh := make(chan bool, 1)
	return func() {
		systray.SetTemplateIcon(icon, icon)

		systray.SetTooltip("Skywire")

		initOpenVPNLinkBtn(conf)
		initAdvancedButton(conf)
		initVpnClientBtn(conf)
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
	contents, err = iconFS.ReadFile(iconName)

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

func initOpenVPNLinkBtn(vc *visorconfig.V1) {
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
			if isVPNExists(vc) {
				mVPNLink.Enable()
				break
			} else {
				mVPNLink.Disable()
			}
		}
	}()
}

func initVpnClientBtn(conf *visorconfig.V1) {
	mVPNClient := systray.AddMenuItem("VPN", "VPN Client Connection")
	// VPN Status
	mVPNStatus := mVPNClient.AddSubMenuItem("Disconnect", "VPN Client Status")
	mVPNStatus.Disable()
	go vpnStatusBtn(mVPNStatus)
	// VPN On/Off Button
	mVPNButton = mVPNClient.AddSubMenuItem("On", "VPN Client Button")
	// VPN Public Servers List
	mVPNServersList := mVPNClient.AddSubMenuItem("Servers", "VPN Client Servers")
	mVPNServers := []*systray.MenuItem{}
	for _, server := range GetAvailPublicVPNServers(conf) {
		mVPNServers = append(mVPNServers, mVPNServersList.AddSubMenuItemCheckbox(server, "", false))
	}
	go serversBtn(mVPNServers)
}

func vpnStatusBtn(vpnStatus *systray.MenuItem) {
	time.Sleep(5 * time.Second)
	vpnStatus.SetTitle("Connecting...")
	time.Sleep(5 * time.Second)
	vpnStatus.SetTitle("Connected")
}

func getHTTPClient(conf *visorconfig.V1, ctx context.Context) *http.Client {
	var serviceURL dmsgget.URL
	serviceURL.Fill(conf.Launcher.ServiceDisc)
	logger := logging.NewMasterLogger()
	if serviceURL.Scheme == "dmsg" {
		var keys cipher.PubKeys
		servers := conf.Dmsg.Servers

		if len(servers) == 0 {
			return &http.Client{}
		}

		pk, sk := conf.PK, conf.SK
		keys = append(keys, pk)
		entries := direct.GetAllEntries(keys, servers)
		dClient := direct.NewClient(entries, logger.PackageLogger("dmsg_http_systray:direct_client"))
		dmsgDC, _, err := direct.StartDmsg(ctx, logger.PackageLogger("dmsg_http_systray:dmsgDC"),
			pk, sk, dClient, dmsg.DefaultConfig())
		if err != nil {
			return &http.Client{}
		}
		dmsgHTTP := http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgDC)}
		return &dmsgHTTP
	}
	return &http.Client{}
}

func serversBtn(servers []*systray.MenuItem) {
	btnChannel := make(chan int)
	for index, server := range servers {
		go func(chn chan int, server *systray.MenuItem, index int) {

			select {
			case <-server.ClickedCh:
				chn <- index
			}
		}(btnChannel, server, index)
	}

	for {
		selectedServer := servers[<-btnChannel]
		serverTempValue := strings.Split(selectedServer.String(), ",")[2]
		serverPK := serverTempValue[2 : len(serverTempValue)-5]
		for _, server := range servers {
			server.Uncheck()
		}
		selectedServer.Check()
		fmt.Println(serverPK)
	}
}

func handleVPNButton(conf *visorconfig.V1) {
	mVPNButton.SetTitle("Off")
}

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
	svrConfig := servicedisc.Config{
		Type:     servicedisc.ServiceTypeVPN,
		PK:       conf.PK,
		SK:       conf.SK,
		DiscAddr: conf.Launcher.ServiceDisc,
	}
	httpC := getHTTPClient(conf, context.Background())
	sdClient := servicedisc.NewClient(log, log, svrConfig, httpC, "")
	vpnServers, err := sdClient.Services(context.Background(), 0)
	if err != nil {
		log.Error("Error getting public vpn servers: ", err)
		return nil
	}
	serverAddrs := make([]string, len(vpnServers))
	for idx, server := range vpnServers {
		serverAddrs[idx] = server.Addr.PubKey().String() + ";" + server.Geo.Country
	}
	fmt.Println(serverAddrs)
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
		case <-mVPNButton.ClickedCh:
			handleVPNButton(conf)
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

func isVPNExists(vc *visorconfig.V1) bool {
	status := false
	for _, app := range vc.Launcher.Apps {
		if app.Name == skyenv.VPNClientName {
			status = true
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
