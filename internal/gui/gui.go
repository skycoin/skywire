//go:build systray
// +build systray

package gui

import (
	"context"
	"embed"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mrpalide/systray"

	"github.com/gen2brain/dlgs"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/pkg/direct"
	dmsgdisc "github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsgget"
	"github.com/skycoin/dmsg/pkg/dmsghttp"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/toqueteos/webbrowser"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
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
	closeDmsgDC   func()
	rpcC          visor.API
)

var (
	guiStopped int32
)

var (
	mAdvancedButton *systray.MenuItem
	mOpenHypervisor *systray.MenuItem
	mVPNClient      *systray.MenuItem
	mVPNStatus      *systray.MenuItem
	mVPNLink        *systray.MenuItem
	mVPNButton      *systray.MenuItem
	mUninstall      *systray.MenuItem
	mQuit           *systray.MenuItem
)

// GetOnGUIReady creates func to run on GUI startup.
func GetOnGUIReady(icon []byte, conf *visorconfig.V1) func() {
	doneCh := make(chan bool, 1)
	logger := logging.NewMasterLogger()
	logger.SetLevel(logrus.InfoLevel)

	httpC := getHTTPClient(conf, context.Background(), logger)
	if isRoot() {
		return func() {
			systray.SetTemplateIcon(icon, icon)
			systray.SetTooltip("Skywire")
			initOpenVPNLinkBtn(conf)
			initAdvancedButton(conf)
			initVpnClientBtn(conf, httpC, logger)
			initQuitBtn()
			go handleRootInteraction(conf, doneCh)
		}
	} else {
		return func() {
			systray.SetTemplateIcon(icon, icon)
			systray.SetTooltip("Skywire")
			initOpenVPNLinkBtn(conf)
			initAdvancedButton(conf)
			initVpnClientBtn(conf, httpC, logger)
			initQuitBtn()
			go handleUserInteraction(conf, doneCh)
		}
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
	//hide the buttons which could launch the browser if the process is run as root
	if isRoot() {
		mAdvancedButton.Hide()
		mOpenHypervisor.Hide()
		return
	}
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
	if isRoot() {
		mVPNLink.Hide()
		return
	}
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

func initVpnClientBtn(conf *visorconfig.V1, httpClient *http.Client, logger *logging.MasterLogger) {

	rpc_logger := logger.PackageLogger("systray:rpc_client")
	hvAddr := getHVAddr(conf)
	for !isHypervisorRunning(hvAddr) {
		rpc_logger.Info("Waiting for RPC to get ready...")
		time.Sleep(2 * time.Second)
	}
	rpcC = rpcClient(conf, rpc_logger)

	mVPNClient := systray.AddMenuItem("VPN", "VPN Client Submenu")
	// VPN Status
	mVPNStatus = mVPNClient.AddSubMenuItem("Status: Disconnect", "VPN Client Status")
	mVPNStatus.Disable()
	go vpnStatusBtn(conf, rpcC)
	// VPN Connect/Disconnect Button
	mVPNButton = mVPNClient.AddSubMenuItem("Connect", "VPN Client Switch Button")
	// VPN Public Servers List
	mVPNServersList := mVPNClient.AddSubMenuItem("Servers", "VPN Client Servers")
	mVPNServers := []*systray.MenuItem{}
	for _, server := range getAvailPublicVPNServers(conf, httpClient, logger.PackageLogger("systray:servers")) {
		mVPNServers = append(mVPNServers, mVPNServersList.AddSubMenuItemCheckbox(server, "", false))
	}
	go serversBtn(conf, mVPNServers, rpcC)
}

func vpnStatusBtn(conf *visorconfig.V1, rpcClient visor.API) {
	lastStatus := 0
	for {
		stats, _ := rpcClient.GetAppConnectionsSummary(skyenv.VPNClientName)
		if len(stats) == 1 {
			if stats[0].IsAlive {
				if lastStatus != 1 {
					mVPNStatus.SetTitle("Status: Connected")
					mVPNButton.SetTitle("Disconnect")
					mVPNButton.Enable()
					lastStatus = 1
				}
			} else {
				if lastStatus != 2 {
					mVPNStatus.SetTitle("Status: Connecting...")
					mVPNButton.SetTitle("Disconnect")
					mVPNButton.Disable()
					lastStatus = 2
				}
			}
		} else {
			if lastStatus != 0 {
				mVPNStatus.SetTitle("Status: Disconnected")
				mVPNButton.SetTitle("Connect")
				mVPNButton.Enable()
				lastStatus = 0
			}
		}
		time.Sleep(3 * time.Second)
	}
}

func serversBtn(conf *visorconfig.V1, servers []*systray.MenuItem, rpcClient visor.API) {
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
			server.Enable()
		}
		selectedServer.Check()
		selectedServer.Disable()
		pk := cipher.PubKey{}
		if err := pk.UnmarshalText([]byte(serverPK)); err != nil {
			continue
		}

		rpcClient.StopApp(skyenv.VPNClientName)
		rpcClient.SetAppPK(skyenv.VPNClientName, pk)
		rpcClient.StartApp(skyenv.VPNClientName)
	}
}

func handleVPNButton(conf *visorconfig.V1, rpcClient visor.API) {
	stats, _ := rpcClient.GetAppConnectionsSummary(skyenv.VPNClientName)
	if len(stats) == 1 {
		if stats[0].IsAlive {
			mVPNStatus.SetTitle("Status: Disconnecting...")
			mVPNButton.Disable()
			mVPNButton.SetTitle("Connect")
			if err := rpcClient.StopApp(skyenv.VPNClientName); err != nil {
				mVPNStatus.SetTitle("Status: Connected")
				mVPNButton.Enable()
				mVPNButton.SetTitle("Disconnect")
			}
		}
	} else {
		mVPNStatus.SetTitle("Status: Connecting...")
		mVPNButton.Disable()
		mVPNButton.SetTitle("Disconnect")
		if err := rpcClient.StartApp(skyenv.VPNClientName); err != nil {
			mVPNStatus.SetTitle("Status: Disconnected")
			mVPNButton.Enable()
			mVPNButton.SetTitle("Connect")
		}
	}
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

// getAvailPublicVPNServers gets all available public VPN server from service discovery URL
func getAvailPublicVPNServers(conf *visorconfig.V1, httpC *http.Client, logger *logging.Logger) []string {

	svrConfig := servicedisc.Config{
		Type:     servicedisc.ServiceTypeVPN,
		PK:       conf.PK,
		SK:       conf.SK,
		DiscAddr: conf.Launcher.ServiceDisc,
	}
	sdClient := servicedisc.NewClient(log, log, svrConfig, httpC, "")
	vpnServers, err := sdClient.Services(context.Background(), 0)
	if err != nil {
		logger.Error("Error getting vpn servers: ", err)
		return nil
	}
	serverAddrs := make([]string, len(vpnServers))
	for idx, server := range vpnServers {
		serverAddrs[idx] = server.Addr.PubKey().String() + ";" + server.Geo.Country
	}
	return serverAddrs
}

func getHTTPClient(conf *visorconfig.V1, ctx context.Context, logger *logging.MasterLogger) *http.Client {
	var serviceURL dmsgget.URL
	serviceURL.Fill(conf.Launcher.ServiceDisc)
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

func isSetVPNClientPKExist(conf *visorconfig.V1) bool {
	for _, v := range conf.Launcher.Apps {
		if v.Name == skyenv.VPNClientName {
			for index := range v.Args {
				if v.Args[index] == "-srv" {
					return true
				}
			}
		}
	}
	return false
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
			handleVPNButton(conf, rpcC)
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

func handleRootInteraction(conf *visorconfig.V1, doneCh chan<- bool) {
	for {
		select {
		case <-mVPNButton.ClickedCh:
			handleVPNButton(conf, rpcC)
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
	closeDmsgDC()
	stop := stopVisorFn
	stopVisorFnMx.Unlock()

	if stop != nil {
		stop()
	}
}

func isHypervisorRunning(addr string) bool {
	// we check if it's up by querying `health` endpoint
	resp, err := http.Get(addr)
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

func rpcClient(conf *visorconfig.V1, logger *logging.Logger) visor.API {
	const rpcDialTimeout = time.Second * 5
	conn, err := net.DialTimeout("tcp", conf.CLIAddr, rpcDialTimeout)
	if err != nil {
		logger.Fatal("RPC connection failed:", err)
	}
	return visor.NewRPCClient(logger, conn, visor.RPCPrefix, 0)
}
