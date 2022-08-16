/*
skywire systray
*/
package main

import (
	"embed"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/bitfield/script"
	cc "github.com/ivanpirog/coloredcobra"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/skycoin/systray"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/skyenv"
)

var (
	isSourcerun   bool
	isDevrun      bool
	remotevisors  []string
	vpnserverpks  []string
	skywirecli    string
	mHV           *systray.MenuItem
	mVisors       *systray.MenuItem
	mVPN          *systray.MenuItem
	mVPNButton    *systray.MenuItem
	mVPNClient    *systray.MenuItem
	mVPNStatus    *systray.MenuItem
	mVPNUI        *systray.MenuItem //nolint:unused
	mPTY          *systray.MenuItem
	mShutdown     *systray.MenuItem
	mStart        *systray.MenuItem
	mAutoconfig   *systray.MenuItem
	mQuit         *systray.MenuItem
	mRemoteVisors []*systray.MenuItem
	mVPNServers   []*systray.MenuItem
	servers       []*systray.MenuItem //nolint
	l             *logging.MasterLogger
	vpnStatusMx   sync.Mutex
	err           error
)

func init() {
	l = logging.NewMasterLogger()
	//disable sorting, flags appear in the order shown here
	rootCmd.Flags().SortFlags = false
	rootCmd.Flags().BoolVarP(&isSourcerun, "src", "s", false, "'go run' using the skywire sources")
	rootCmd.Flags().BoolVarP(&isDevrun, "dev", "d", false, "show remote visors & dmsghttp ui")

}

var rootCmd = &cobra.Command{
	Use:                "skywire-systray",
	Short:              "skywire systray",
	SilenceErrors:      true,
	SilenceUsage:       true,
	DisableSuggestions: true,
	//	PreRun: func(cmd *cobra.Command, _ []string) {
	//	},
	Run: func(cmd *cobra.Command, args []string) {
		//skywire-cli command to use
		if !isSourcerun {
			skywirecli = "skywire-cli"
		} else {
			skywirecli = "go run cmd/skywire-cli/skywire-cli.go"
		}
		onExit := func() {
			now := time.Now()
			fmt.Println("Exit at", now.String())
		}
		systray.Run(onReady, onExit)
	},
}

// Execute executes root command.
func Execute() {
	cc.Init(&cc.Config{
		RootCmd:       rootCmd,
		Headings:      cc.HiBlue + cc.Bold, //+ cc.Underline,
		Commands:      cc.HiBlue + cc.Bold,
		CmdShortDescr: cc.HiBlue,
		Example:       cc.HiBlue + cc.Italic,
		ExecName:      cc.HiBlue + cc.Bold,
		Flags:         cc.HiBlue + cc.Bold,
		//FlagsDataType: cc.HiBlue,
		FlagsDescr:      cc.HiBlue,
		NoExtraNewlines: true,
		NoBottomNewline: true,
	})
	if err = rootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}

//go:embed icons/*
var iconFS embed.FS

func main() {
	Execute()
}

func onReady() {
	l := logging.NewMasterLogger()
	sysTrayIcon, err := ReadSysTrayIcon()
	if err != nil {
		l.WithError(err).Fatalln("Failed to read system tray icon")
	}
	systray.SetTemplateIcon(sysTrayIcon, sysTrayIcon)
	systray.SetTitle("Skywire")
	systray.SetTooltip("Skywire")
	mQuit = systray.AddMenuItem("Quit", "Quit the whole app")

	//check that the visor is running and responds over RPC
	visor, err := script.Exec(skywirecli + ` visor pk`).Match("FATAL").String()
	if err != nil {
		l.WithError(err).Warn("Failed to get visor public key")
		//visor should be empty string if the visor is running
		visor = " "
	}
	systray.SetTemplateIcon(sysTrayIcon, sysTrayIcon)
	systray.SetTitle("Skywire")

	//Top level menu
	//mHV launches the hypervisor with `skywire-cli hv ui`
	mHV = systray.AddMenuItem("Hypervisor", "Hypervisor")
	mHV.Hide()
	//mPTY launches the dmsgpty ui with `skywire-cli hv dmsg ui`
	mPTY = systray.AddMenuItem("DMSGPTY UI", "DMSGPTY UI")
	mPTY.Hide()
	//mVPNUI launches the VPN ui with `skywire-cli hv dmsg ui`
	mVPNUI = systray.AddMenuItem("VPN UI", "VPN UI")
	mVPNUI.Hide()
	//mVisors menu to access dmsgpty ui for connected remote visors
	mVisors = systray.AddMenuItem("Visors", "Visors")
	mVisors.Hide()
	//mVPNClient contains the vpn menu and server list submenu
	mVPNClient = systray.AddMenuItem("VPN", "VPN Client Submenu")
	mVPNClient.Hide()
	//mStart start a stopped the visor
	mStart = systray.AddMenuItem("Start", "Start")
	mStart.Hide()
	//mAutoconfig run the autoconfig script provided by the package or installer
	mAutoconfig = systray.AddMenuItem("Autoconfig", "Autoconfig")
	mAutoconfig.Hide()
	//mShutdown shut down a running visor
	mShutdown = systray.AddMenuItem("Shutdown", "Shutdown")
	mShutdown.Hide()

	//Sub menus
	//mVPNStatus shows current VPN connection status derived from `skywire-cli visor app info`
	mVPNStatus = mVPNClient.AddSubMenuItem("Status: Disconnected", "VPN Client Status")
	mVPNStatus.Disable()
	//mVPNButton VPN on / off button
	mVPNButton = mVPNClient.AddSubMenuItem("Connect", "VPN Client Switch Button")
	//mVPN is the list of VPN server public keys returned by `skywire-cli hv vpn list`
	mVPN = mVPNClient.AddSubMenuItem("VPN Servers", "VPN Servers")

	if visor != "" {
		ToggleOff()
	} else {
		if isDevrun {
			//check for connected visors
			visors, err := script.Exec(skywirecli + ` dmsgpty list`).String()
			if err != nil {
				l.WithError(err).Warn("Failed to fetch connected visors " + visors)
			}
			remotevisors = strings.Split(visors, "\n")
			for i := range remotevisors {
				if remotevisors[i] != "" {
					l.Info("remote visors: " + remotevisors[i])
				}
			}
			mRemoteVisors = []*systray.MenuItem{}
			for _, v := range remotevisors {
				if v != "" {
					mRemoteVisors = append(mRemoteVisors, mVisors.AddSubMenuItem(v, ""))
				}
			}
			go visorsBtn(mRemoteVisors)
		}
		go vpnStatusBtn()
		//check for available vpn servers
		vpnlistpks, err := script.Exec(skywirecli + ` vpn list -y`).String()
		if err != nil {
			l.WithError(err).Warn("Failed to fetch vpn servers")
		}
		vpnlistpks = strings.Trim(vpnlistpks, "[")
		vpnlistpks = strings.Trim(vpnlistpks, "]")
		vpnserverpks = strings.Split(vpnlistpks, "\n")
		mVPNServers = []*systray.MenuItem{}
		for _, v := range vpnserverpks {
			if v != "" {
				mVPNServers = append(mVPNServers, mVPN.AddSubMenuItemCheckbox(v, "", false))
			}
		}
		go serversBtn(mVPNServers)
		ToggleOn()
	}
	systray.AddSeparator()
	//this blank item retains minimum text displacement

	go func() {
		<-mQuit.ClickedCh
		fmt.Println("Requesting quit")
		systray.Quit()
		fmt.Println("Finished quitting")
	}()
	go func() {
		for {
			select {
			case <-mHV.ClickedCh:
				_, err = script.Exec(skywirecli + ` visor hvui`).Stdout()
				if err != nil {
					l.WithError(err).Warn("Failed to open hypervisor UI")
				}
			case <-mVPNUI.ClickedCh:
				_, err = script.Exec(skywirecli + ` vpn ui`).Stdout()
				if err != nil {
					l.WithError(err).Warn("Failed to open VPN UI")
				}
			case <-mVPNButton.ClickedCh:
				handleVPNButton()
			case <-mPTY.ClickedCh:
				_, err = script.Exec(skywirecli + ` dmsg ui`).Stdout()
				if err != nil {
					l.WithError(err).Warn("Failed to open dmsgpty UI")
				}
			case <-mStart.ClickedCh:
				_, err = script.Exec(`systemctl enable --now skywire`).Stdout()
				if err != nil {
					l.WithError(err).Warn("Failed to start skywire")
				} else {
					ToggleOn()
				}
			case <-mAutoconfig.ClickedCh:
				//execute the skywire-autoconfig script includedwith the skywire package
				_, err = script.Exec(`exo-open --launch TerminalEmulator bash -c 'sudo SKYBIAN=true skywire-autoconfig && sleep 5'`).Stdout()
				if err != nil {
					l.WithError(err).Warn("Failed to generate skywire configuration")
				} else {
					ToggleOn()
				}
			case <-mShutdown.ClickedCh:
				if skyenv.OS == "linux" {
					_, _ = script.Exec(`systemctl disable --now skywire`).Stdout() //nolint:errcheck
					ToggleOff()
				} else {
					l.Warn("shutdown of services not yet implemented on windows / mac")
				}
				_, err = script.Exec(skywirecli + ` visor halt 2> /dev/null`).Stdout()
				if err != nil {
					l.WithError(err).Warn("Failed to stop skywire")
				} else {
					ToggleOff()
				}
			case <-mQuit.ClickedCh:
				systray.Quit()
				fmt.Println("Quit2 now...")
				return
			}
		}
	}()
}

// ReadSysTrayIcon reads system tray icon.
func ReadSysTrayIcon() (contents []byte, err error) {
	contents, err = iconFS.ReadFile("icons/icon.png")
	if err != nil {
		err = fmt.Errorf("failed to read icon: %w", err)
	}
	return contents, err
}

func visorsBtn(mRemoteVisors []*systray.MenuItem) {
	btnChannel := make(chan int)
	for index, remotevisor := range mRemoteVisors {
		go func(chn chan int, remotevisor *systray.MenuItem, index int) {
			for { //nolint
				select {
				case <-remotevisor.ClickedCh:
					l.Info("opening dmsgpty ui to visor: " + remotevisors[index])
					_, err = script.Exec(skywirecli + ` hv dmsg ui -v ` + remotevisors[index]).Stdout()
					if err != nil {
						l.WithError(err).Warn("Failed to open dmsgpty UI")
					}
					chn <- index
				}
			}
		}(btnChannel, remotevisor, index)
	}
}

func serversBtn(servers []*systray.MenuItem) { //nolint
	btnChannel := make(chan int)
	for index, server := range servers { //nolint
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
		for _, server := range servers { //nolint
			server.Uncheck()
			server.Enable()
		}
		selectedServer.Check()
		selectedServer.Disable()
		//		pk := cipher.PubKey{}
		//		if err := pk.UnmarshalText([]byte(serverPK)); err != nil {
		//			continue
		//		}
		stats, err := script.Exec(skywirecli + ` vpn status`).String()
		if err != nil {
			break
		}
		if stats == "running\n" {
			_, err = script.Exec(skywirecli + ` vpn stop`).Stdout()
			if err != nil {
				l.WithError(err).Warn("Failed to stop vpn-client")
			}
		}
		_, err = script.Exec(`bash -c 'export VPNSERVERPK=` + serverPK + ` ; ` + skywirecli + ` vpn start ${VPNSERVERPK%% *}'`).Stdout()
		if err != nil {
			l.WithError(err).Warn("Failed to start vpn-client")
		}
	}
}

func vpnStatusBtn() {
	for {
		vpnStatusMx.Lock()
		stats, err := script.Exec(skywirecli + ` vpn status`).String()
		if err != nil {
			mVPNStatus.SetTitle("Status: Disconnected")
			mVPNButton.SetTitle("Connect")
			break
		}
		if stats == "running\n" {
			mVPNStatus.SetTitle("Status: Connected")
			mVPNButton.SetTitle("Disconnect")
		}
		if stats == "stopped\n" {
			mVPNStatus.SetTitle("Status: Disconnected")
			mVPNButton.SetTitle("Connect")
		}
		if stats == "error\n" {
			mVPNStatus.SetTitle("Status: Error")
			mVPNButton.SetTitle("Connect")

		}
		vpnStatusMx.Unlock()
		time.Sleep(2 * time.Second)
	}
}

func handleVPNButton() { //nolint
	appstate, err := script.Exec(skywirecli + ` vpn status`).String()
	if err != nil {
		l.WithError(err).Warn("Failed to get vpn-client status")
	}
	if appstate == "running\n" {
		_, err = script.Exec(skywirecli + ` vpn stop `).Stdout()
		if err != nil {
			l.WithError(err).Warn("Failed to stop vpn-client")
		}
	} else {
		_, err = script.Exec(skywirecli + ` vpn start `).Stdout()
		if err != nil {
			l.WithError(err).Warn("Failed to start vpn-client")
		}
	}
}

// ToggleOn menu when skywire visor is running
func ToggleOn() {
	//check for connected visors
	visors, err := script.Exec(skywirecli + ` dmsgpty list`).String()
	if err != nil {
		l.WithError(err).Warn("Failed to fetch connected visors " + visors)
	}
	if isDevrun {
		mPTY.Show()
		if (visors != "") && (visors != "\n") {
			mVisors.Show()
		} else {
			mVisors.Hide()
		}
	} else {
		mVisors.Hide()
		mPTY.Hide()
	}
	mHV.Show()
	mVPNUI.Show()
	mVPN.Show()
	mVPNClient.Show()
	mStart.Hide()
	mAutoconfig.Hide()
	mShutdown.Show()
	mQuit.Show()
}

// ToggleOff menu when skywire visor is NOT running
func ToggleOff() {
	mHV.Hide()
	mPTY.Hide()
	mVPNUI.Hide()
	mVPNClient.Hide()
	mVisors.Hide()
	mShutdown.Hide()

	mStart.Show()
	if skyenv.OS == "linux" {
		mAutoconfig.Show()
	}
	mQuit.Show()
}
