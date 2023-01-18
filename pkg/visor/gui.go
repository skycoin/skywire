//go:build !withoutsystray
// +build !withoutsystray

// Package visor pkg/visor/gui.go
package visor

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/systray"

	"github.com/gen2brain/dlgs"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/pkg/direct"
	dmsgdisc "github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsgget"
	"github.com/skycoin/dmsg/pkg/dmsghttp"
	"github.com/toqueteos/webbrowser"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
	"github.com/skycoin/skywire/static/icons"
)

// TODO @alexadhy : Show VPN status, list all vpn servers, quick dial

var iconFS = &icons.Assets

var (
	stopVisorFnMx sync.Mutex
	stopVisorFn   func()
	closeDmsgDC   func()
	rpcC          API
	vpnLastStatus int
)

var (
	guiStopped int32
)

var (
	mOpenHypervisor *systray.MenuItem
	mVPNClient      *systray.MenuItem
	mVPNStatus      *systray.MenuItem
	mVPNLink        *systray.MenuItem
	mVPNButton      *systray.MenuItem
	mUninstall      *systray.MenuItem
	mQuit           *systray.MenuItem
	vpnStatusMx     sync.Mutex
)

// getOnGUIReady creates func to run on GUI startup.
func getOnGUIReady(icon []byte, conf *visorconfig.V1) func() {
	doneCh := make(chan bool, 1)
	logger := logging.NewMasterLogger()
	logger.SetLevel(logrus.InfoLevel)

	httpC := getSystrayHTTPClient(context.Background(), conf, logger)

	if isRoot() {
		return func() {
			systray.SetTemplateIcon(icon, icon)
			systray.SetTooltip("Skywire")
			initUIBtns(conf)
			initVpnClientBtn(conf, httpC, logger)
			initAdvancedButton()
			initQuitBtn()
			go handleRootInteraction(doneCh)
		}
	}
	return func() {
		systray.SetTemplateIcon(icon, icon)
		systray.SetTooltip("Skywire")
		initUIBtns(conf)
		initVpnClientBtn(conf, httpC, logger)
		initAdvancedButton()
		initQuitBtn()
		go handleUserInteraction(conf, doneCh)
	}
}

// onGUIQuit is executed on GUI exit.
func onGUIQuit() {
}

// readSysTrayIcon reads system tray icon.
func readSysTrayIcon() (contents []byte, err error) {
	contents, err = iconFS.ReadFile(iconName)

	if err != nil {
		err = fmt.Errorf("failed to read icon: %w", err)
	}

	return contents, err
}

// SetStopVisorFn sets function to stop running
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

func initAdvancedButton() {
	mAdvancedButton := systray.AddMenuItem("Advanced", "Advanced Menu")
	mUninstall = mAdvancedButton.AddSubMenuItem("Uninstall", "Uninstall Application")

	// if it's not installed via package, hide the uninstall button
	if !checkIsPackage() {
		mAdvancedButton.Hide()
	}
}

func initUIBtns(vc *visorconfig.V1) {
	mOpenHypervisor = systray.AddMenuItem("Open Hypervisor UI", "Open Hypervisor")
	mVPNLink = systray.AddMenuItem("Open VPN UI", "Open VPN UI in browser")
	hvAddr := getHVAddr(vc)
	if isRoot() {
		mVPNLink.Hide()
		mOpenHypervisor.Hide()
		return
	}
	mVPNLink.Disable()
	mOpenHypervisor.Disable()

	// if not hypervisor, both buttons no need to start
	if hvAddr == "" {
		return
	}

	// wait for the vpn client to start in the background
	// if it's not started or if it isn't alive just disable the link.
	go func() {
		t := time.NewTicker(1 * time.Second)
		defer t.Stop()

		// we simply wait till the hypervisor is up
		for {
			<-t.C
			if mOpenHypervisor.Disabled() {
				if isHypervisorRunning(hvAddr) {
					mOpenHypervisor.Enable()
				}
			} else {
				if isVPNExists(vc) {
					mVPNLink.Enable()
					return
				}
			}
		}
	}()
}

func initVpnClientBtn(conf *visorconfig.V1, httpClient *http.Client, logger *logging.MasterLogger) {
	rpcLogger := logger.PackageLogger("systray:rpc_client")
	rpcC = rpcClientSystray(conf, rpcLogger)

	mVPNClient = systray.AddMenuItem("VPN", "VPN Client Submenu")
	// VPN Status
	mVPNStatus = mVPNClient.AddSubMenuItem("Status: Disconnected", "VPN Client Status")
	mVPNStatus.Disable()
	go vpnStatusBtn(rpcC)
	// VPN Connect/Disconnect Button
	mVPNButton = mVPNClient.AddSubMenuItem("Connect", "VPN Client Switch Button")
	// VPN Public Servers List
	mVPNServersList := mVPNClient.AddSubMenuItem("Servers", "VPN Client Servers")
	mVPNServers := []*systray.MenuItem{}
	for _, server := range getAvailPublicVPNServers(conf, httpClient, logger.PackageLogger("systray:servers")) {
		mVPNServers = append(mVPNServers, mVPNServersList.AddSubMenuItemCheckbox(server, "", false))
	}
	go serversBtn(mVPNServers, rpcC)
}

func vpnStatusBtn(rpcClient API) {
	for {
		vpnStatusMx.Lock()
		stats, _ := rpcClient.GetAppConnectionsSummary(visorconfig.VPNClientName) //nolint
		if len(stats) == 1 {
			if stats[0].IsAlive {
				if vpnLastStatus != 1 {
					mVPNStatus.SetTitle("Status: Connected")
					mVPNButton.SetTitle("Disconnect")
					vpnLastStatus = 1
				}
			} else {
				if vpnLastStatus != 2 {
					mVPNStatus.SetTitle("Status: Connecting")
					mVPNButton.SetTitle("Disconnect")
					vpnLastStatus = 2
				}
			}
		} else {
			if vpnLastStatus != 0 {
				if vpnLastStatus == 2 || vpnLastStatus == 3 {
					mVPNStatus.SetTitle("Status: Errored")
				} else {
					mVPNStatus.SetTitle("Status: Disconnected")
				}
				mVPNButton.SetTitle("Connect")
				vpnLastStatus = 0
			}
		}
		vpnStatusMx.Unlock()
		time.Sleep(2 * time.Second)
	}
}

func serversBtn(servers []*systray.MenuItem, rpcClient API) {
	btnChannel := make(chan int)
	for index, server := range servers {
		go func(chn chan int, server *systray.MenuItem, index int) {
			for { //nolint
				select {
				case <-server.ClickedCh:
					chn <- index
				}
			}
		}(btnChannel, server, index)
	}

	for {
		selectedServer := servers[<-btnChannel]
		serverTempValue := strings.Split(selectedServer.String(), ",")[2]
		serverPK := serverTempValue[2 : len(serverTempValue)-7]
		for _, server := range servers {
			server.Uncheck()
			server.Enable()
		}
		selectedServer.Check()
		selectedServer.Disable()
		pk := cipher.PubKey{}
		if err := pk.UnmarshalText([]byte(serverPK)); err != nil {
			continue
		}

		rpcClient.StopApp(visorconfig.VPNClientName)      //nolint
		rpcClient.SetAppPK(visorconfig.VPNClientName, pk) //nolint
		vpnStatusMx.Lock()
		vpnLastStatus = 3
		vpnStatusMx.Unlock()
		rpcClient.StartApp(visorconfig.VPNClientName) //nolint
	}
}

func handleVPNButton(rpcClient API) {
	stats, _ := rpcClient.GetAppConnectionsSummary(visorconfig.VPNClientName) //nolint
	if len(stats) == 1 {
		rpcClient.StopApp(visorconfig.VPNClientName) //nolint
	} else {
		vpnStatusMx.Lock()
		vpnLastStatus = 3
		vpnStatusMx.Unlock()
		rpcClient.StartApp(visorconfig.VPNClientName) //nolint
	}
}

func handleVPNLinkButton(conf *visorconfig.V1) {
	vpnAddr := getVPNAddr(conf)

	if vpnAddr == "" {
		mVPNLink.Disable()
		mLog.Error("empty vpn URL address")
		return // do nothing
	}

	if err := webbrowser.Open(vpnAddr); err != nil {
		mLog.WithError(err).Error("failed to open link")
	}
}

// getAvailPublicVPNServers gets all available public VPN server from service discovery URL
func getAvailPublicVPNServers(conf *visorconfig.V1, httpC *http.Client, logger *logging.Logger) []string {
	svrConfig := servicedisc.Config{
		Type:     servicedisc.ServiceTypeVPN,
		PK:       conf.PK,
		SK:       conf.SK,
		DiscAddr: conf.Launcher.ServiceDisc,
	}
	sdClient := servicedisc.NewClient(mLog, mLog, svrConfig, httpC, "")
	vpnServers, err := sdClient.Services(context.Background(), 0, "", "")
	if err != nil {
		logger.Error("Error getting vpn servers: ", err)
		return nil
	}
	serverAddrs := make([]string, len(vpnServers))
	for idx, server := range vpnServers {
		if server.Geo != nil {
			serverAddrs[idx] = server.Addr.PubKey().String() + " | " + server.Geo.Country
		} else {
			serverAddrs[idx] = server.Addr.PubKey().String() + " | NA"
		}
	}
	return serverAddrs
}

func getSystrayHTTPClient(ctx context.Context, conf *visorconfig.V1, logger *logging.MasterLogger) *http.Client {
	var serviceURL dmsgget.URL
	serviceURL.Fill(conf.Launcher.ServiceDisc) //nolint
	if serviceURL.Scheme == "dmsg" {
		var keys cipher.PubKeys
		servers := conf.Dmsg.Servers
		var delegatedServers []cipher.PubKey

		if len(servers) == 0 {
			return &http.Client{}
		}

		pk, sk := cipher.GenerateKeyPair()
		keys = append(keys, pk)
		entries := direct.GetAllEntries(keys, servers)
		dClient := direct.NewClient(entries, logger.PackageLogger("systray:dmsghttp_direct_client"))
		dmsgDC, closeDmsg, err := direct.StartDmsg(ctx, logger.PackageLogger("systray:dsmghttp_dmsgDC"),
			pk, sk, dClient, dmsg.DefaultConfig())
		if err != nil {
			return &http.Client{}
		}
		dmsgHTTP := http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgDC)}

		servers, err = dClient.AvailableServers(ctx)
		if err != nil {
			closeDmsg()
			return &http.Client{}
		}

		for _, server := range servers {
			delegatedServers = append(delegatedServers, server.Static)
		}

		clientEntry := &dmsgdisc.Entry{
			Client: &dmsgdisc.Client{
				DelegatedServers: delegatedServers,
			},
			Static: serviceURL.Addr.PK,
		}

		err = dClient.PostEntry(ctx, clientEntry)
		if err != nil {
			closeDmsg()
			return &http.Client{}
		}
		closeDmsgDC = closeDmsg
		return &dmsgHTTP
	}
	closeDmsgDC = func() {}
	return &http.Client{}
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
			handleVPNButton(rpcC)
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

func handleRootInteraction(doneCh chan<- bool) {
	for {
		select {
		case <-mVPNButton.ClickedCh:
			handleVPNButton(rpcC)
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
		mLog.WithError(err).Errorln("Failed to open hypervisor")
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
			mLog.WithError(err).Errorln("Failed to run deinstaller")
			return
		}
		systray.Quit()
	}
}

func stopVisor() {
	stopVisorFnMx.Lock()
	closeDmsgDC()
	stop := stopVisorFn
	stopVisorFnMx.Unlock()

	if stop != nil {
		stop()
	}
}

func isHypervisorRunning(addr string) bool {
	// we check if it's up by querying `health` endpoint
	resp, err := http.Get(addr) //nolint
	if err != nil {
		// hypervisor is not running in this case
		return false
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			mLog.WithError(err).Errorln("Failed to close hypervisor response body")
		}
	}()

	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		mLog.WithError(err).Errorln("Failed to discard hypervisor response body")
	}

	return true
}

func openHypervisor(conf *visorconfig.V1) error {
	hvAddr := getHVAddr(conf)
	if hvAddr == "" {
		return nil
	}

	mLog.Infof("Opening hypervisor at %s", hvAddr)

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
		if app.Name == visorconfig.VPNClientName {
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

func rpcClientSystray(conf *visorconfig.V1, logger *logging.Logger) API {
	var conn net.Conn
	var err error
	var rpcConnected bool
	logger.Info("Connecting to RPC")
	for !rpcConnected {
		conn, err = net.Dial("tcp", conf.CLIAddr)
		if err != nil {
			logger.Warn("RPC connection failed. Try again in 2 seconds.")
		} else {
			rpcConnected = true
		}
		time.Sleep(2 * time.Second)
	}
	logger.Info("RPC Connection established")
	return NewRPCClient(logger, conn, RPCPrefix, 0)
}
