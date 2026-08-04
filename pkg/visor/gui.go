//go:build !withoutsystray
// +build !withoutsystray

// Package visor pkg/visor/gui.go c3-vis-core
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

	"fyne.io/systray"
	"github.com/gen2brain/dlgs"
	"github.com/sirupsen/logrus"
	"github.com/toqueteos/webbrowser"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/direct"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgcurl"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
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
)

var (
	guiStopped int32
)

var (
	mOpenHypervisor *systray.MenuItem
	mVPNLink        *systray.MenuItem
	mUninstall      *systray.MenuItem
	mSuspend        *systray.MenuItem // toggle: suspend / resume the visor
	mAutoconnect    *systray.MenuItem // checkbox: public autoconnect on/off
	mQuit           *systray.MenuItem

	vpnTray   *appTray
	proxyTray *appTray
)

// appTray bundles the submenu items for a client app (VPN client or
// skysocks proxy client) so the status poller, public-server list, and
// connect/disconnect button can be shared between them instead of being
// duplicated per app.
type appTray struct {
	name    string // launcher app name (skyenv.VPNClientName / SkysocksClientName)
	svcType string // servicedisc.ServiceType* used to list public servers

	menu    *systray.MenuItem
	status  *systray.MenuItem
	button  *systray.MenuItem
	servers []*systray.MenuItem

	mx         sync.Mutex
	lastStatus int
}

// getOnGUIReady creates func to run on GUI startup.
func getOnGUIReady(icon []byte, conf *visorconfig.V1) func() {
	doneCh := make(chan bool, 1)
	logger := logging.NewMasterLogger()
	logger.SetLevel(logrus.InfoLevel)

	httpC := getSystrayHTTPClient(context.Background(), conf, logger)

	return func() {
		systray.SetTemplateIcon(icon, icon)
		systray.SetTooltip("Skywire")
		initUIBtns(conf)
		initClientBtns(conf, httpC, logger)
		initVisorControlBtns(conf)
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
	mVPNLink.Disable()
	mOpenHypervisor.Disable()

	// These two items open browser UIs. Hide them when there is no
	// hypervisor to link to, or when the tray runs as root (a root
	// process can't open the desktop user's browser). The RPC-driven
	// controls — client connect/disconnect, suspend/resume, public
	// autoconnect — stay available in every mode, so a root/headless
	// visor still gets the full set of controls, just not the links.
	if hvAddr == "" || isRoot() {
		mOpenHypervisor.Hide()
		mVPNLink.Hide()
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

// initClientBtns builds the VPN and skysocks-proxy client submenus. Both
// share the same shape (status line, connect/disconnect button, public
// server picker) via appTray, so the poller/servers/button logic below is
// written once and driven for each.
func initClientBtns(conf *visorconfig.V1, httpClient *http.Client, logger *logging.MasterLogger) {
	rpcLogger := logger.PackageLogger("systray:rpc_client")
	rpcC = rpcClientSystray(conf, rpcLogger)

	vpnTray = &appTray{name: skyenv.VPNClientName, svcType: servicedisc.ServiceTypeVPN}
	proxyTray = &appTray{name: skyenv.SkysocksClientName, svcType: servicedisc.ServiceTypeProxy}

	initClientTray(vpnTray, "VPN", "VPN client", conf, httpClient, logger)
	initClientTray(proxyTray, "Proxy", "Skysocks proxy client", conf, httpClient, logger)
}

// initClientTray wires one appTray's submenu items and starts its pollers.
func initClientTray(t *appTray, label, desc string, conf *visorconfig.V1, httpClient *http.Client, logger *logging.MasterLogger) {
	t.menu = systray.AddMenuItem(label, desc+" submenu")
	t.status = t.menu.AddSubMenuItem("Status: Disconnected", desc+" status")
	t.status.Disable()
	t.button = t.menu.AddSubMenuItem("Connect", desc+" connect/disconnect")

	serversList := t.menu.AddSubMenuItem("Servers", desc+" public servers")
	for _, server := range getAvailPublicServers(conf, t.svcType, httpClient, logger.PackageLogger("systray:servers")) {
		t.servers = append(t.servers, serversList.AddSubMenuItemCheckbox(server, "", false))
	}

	go appStatusBtn(t, rpcC)
	go serversBtn(t, rpcC)
}

// appStatusBtn polls the client app's connection summary and reflects it
// in the tray's status line + button title. lastStatus: 0=off, 1=alive,
// 2=connecting, 3=just-requested.
func appStatusBtn(t *appTray, rpcClient API) {
	for {
		t.mx.Lock()
		stats, _ := rpcClient.GetAppConnectionsSummary(t.name) //nolint:errcheck
		if len(stats) == 1 {
			if stats[0].IsAlive {
				if t.lastStatus != 1 {
					t.status.SetTitle("Status: Connected")
					t.button.SetTitle("Disconnect")
					t.lastStatus = 1
				}
			} else {
				if t.lastStatus != 2 {
					t.status.SetTitle("Status: Connecting")
					t.button.SetTitle("Disconnect")
					t.lastStatus = 2
				}
			}
		} else {
			if t.lastStatus != 0 {
				if t.lastStatus == 2 || t.lastStatus == 3 {
					t.status.SetTitle("Status: Errored")
				} else {
					t.status.SetTitle("Status: Disconnected")
				}
				t.button.SetTitle("Connect")
				t.lastStatus = 0
			}
		}
		t.mx.Unlock()
		time.Sleep(2 * time.Second)
	}
}

func serversBtn(t *appTray, rpcClient API) {
	btnChannel := make(chan int)
	for index, server := range t.servers {
		go func(chn chan int, server *systray.MenuItem, index int) {
			for range server.ClickedCh {
				chn <- index
			}
		}(btnChannel, server, index)
	}

	for {
		selectedServer := t.servers[<-btnChannel]
		serverTempValue := strings.Split(selectedServer.String(), ",")[2]
		serverPK := serverTempValue[2 : len(serverTempValue)-7]
		for _, server := range t.servers {
			server.Uncheck()
			server.Enable()
		}
		selectedServer.Check()
		selectedServer.Disable()
		pk := cipher.PubKey{}
		if err := pk.UnmarshalText([]byte(serverPK)); err != nil {
			continue
		}

		rpcClient.StopApp(t.name)      //nolint:errcheck,gosec
		rpcClient.SetAppPK(t.name, pk) //nolint:errcheck,gosec
		t.mx.Lock()
		t.lastStatus = 3
		t.mx.Unlock()
		rpcClient.StartApp(t.name) //nolint:errcheck,gosec
	}
}

func handleAppButton(t *appTray, rpcClient API) {
	stats, _ := rpcClient.GetAppConnectionsSummary(t.name) //nolint:errcheck
	if len(stats) == 1 {
		rpcClient.StopApp(t.name) //nolint:errcheck,gosec
	} else {
		t.mx.Lock()
		t.lastStatus = 3
		t.mx.Unlock()
		rpcClient.StartApp(t.name) //nolint:errcheck,gosec
	}
}

// initVisorControlBtns adds the visor-level controls: a Suspend/Resume
// toggle (pause the whole visor without stopping the service — see
// API.Suspend) and a public-autoconnect checkbox.
func initVisorControlBtns(conf *visorconfig.V1) {
	mSuspend = systray.AddMenuItem("Suspend visor", "Tear down networking but keep the visor process (resume without root)")
	mAutoconnect = systray.AddMenuItemCheckbox("Public autoconnect", "Automatically connect to public visors", conf.Transport != nil && conf.Transport.PublicAutoconnect)
	go visorControlPoll(rpcC)
}

// visorControlPoll keeps the Suspend/Resume toggle label and the
// autoconnect checkbox in sync with the visor's actual state.
func visorControlPoll(rpcClient API) {
	for {
		if rpcClient != nil {
			if suspended, err := rpcClient.IsSuspended(); err == nil {
				if suspended {
					mSuspend.SetTitle("Resume visor")
				} else {
					mSuspend.SetTitle("Suspend visor")
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
}

// handleSuspendButton toggles the visor between running and suspended.
func handleSuspendButton(rpcClient API) {
	if rpcClient == nil {
		return
	}
	suspended, err := rpcClient.IsSuspended()
	if err != nil {
		mLog.WithError(err).Error("failed to query suspend state")
		return
	}
	if suspended {
		if err := rpcClient.Resume(); err != nil {
			mLog.WithError(err).Error("failed to resume visor")
		}
		return
	}
	if err := rpcClient.Suspend(); err != nil {
		mLog.WithError(err).Error("failed to suspend visor")
	}
}

// handleAutoconnectButton toggles public autoconnect and reflects the new
// state in the checkbox.
func handleAutoconnectButton(rpcClient API) {
	if rpcClient == nil {
		return
	}
	enable := !mAutoconnect.Checked()
	if err := rpcClient.SetPublicAutoconnect(enable); err != nil {
		mLog.WithError(err).Error("failed to toggle public autoconnect")
		return
	}
	if enable {
		mAutoconnect.Check()
	} else {
		mAutoconnect.Uncheck()
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

// getAvailPublicServers gets all available public servers of the given
// service type (VPN or proxy) from the service-discovery URL.
func getAvailPublicServers(conf *visorconfig.V1, svcType string, httpC *http.Client, logger *logging.Logger) []string {
	svrConfig := servicedisc.Config{
		Type:     svcType,
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
	var serviceURL dmsgcurl.URL
	serviceURL.Fill(conf.Launcher.ServiceDisc) //nolint:errcheck,gosec
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
		case <-vpnTray.button.ClickedCh:
			handleAppButton(vpnTray, rpcC)
		case <-proxyTray.button.ClickedCh:
			handleAppButton(proxyTray, rpcC)
		case <-mVPNLink.ClickedCh:
			handleVPNLinkButton(conf)
		case <-mSuspend.ClickedCh:
			handleSuspendButton(rpcC)
		case <-mAutoconnect.ClickedCh:
			handleAutoconnectButton(rpcC)
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
	resp, err := http.Get(addr) //nolint:gosec
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
