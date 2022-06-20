/*
skywire systray
*/
package main

import (
	"embed"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bitfield/script"
	cc "github.com/ivanpirog/coloredcobra"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/skycoin/systray"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/internal/gui"
	"github.com/skycoin/skywire/pkg/skyenv"
)

var (
	sourcerun     bool
	remotevisors  []string
	skywirecli    string
	mHV           *systray.MenuItem
	mVisors       *systray.MenuItem
	mVPN          *systray.MenuItem
	mPTY          *systray.MenuItem
	mShutdown     *systray.MenuItem
	mStart        *systray.MenuItem
	mAutoconfig   *systray.MenuItem
	mQuit         *systray.MenuItem
	mRemoteVisors []*systray.MenuItem
	servers       []*systray.MenuItem //nolint:unused
	l             *logging.MasterLogger
	err           error
)

func init() {
	l = logging.NewMasterLogger()
	//disable sorting, flags appear in the order shown here
	rootCmd.Flags().SortFlags = false
	rootCmd.Flags().BoolVarP(&sourcerun, "src", "s", false, "'go run' external commands from the skywire sources")

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
		if !sourcerun {
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
	go func() {
		<-mQuit.ClickedCh
		fmt.Println("Requesting quit")
		systray.Quit()
		fmt.Println("Finished quitting")
	}()
	go func() {
		//check that the visor is running and responds over RPC
		visor, err := script.Exec(skywirecli + ` visor pk`).Match("FATAL").String()
		if err != nil {
			l.WithError(err).Warn("Failed to get visor public key")
			//visor should be empty string if the visor is running
			visor = " "
		}
		systray.SetTemplateIcon(sysTrayIcon, sysTrayIcon)
		systray.SetTitle("Skywire")

		mHV = systray.AddMenuItem("Hypervisor", "Hypervisor")
		mVisors = systray.AddMenuItem("Visors", "Visors")

		//check for connected visors
		visors, err := script.Exec(skywirecli + ` dmsgpty list`).String()
		if err != nil {
			l.WithError(err).Warn("Failed to fetch connected visors " + visors)
		}
		remotevisors = strings.Split(visors, "\n")

		for i := range remotevisors {
			l.Info("visors: " + remotevisors[i])
		}
		mRemoteVisors = []*systray.MenuItem{}
		for _, v := range remotevisors {
			mRemoteVisors = append(mRemoteVisors, mVisors.AddSubMenuItem(v, ""))
		}
		go visorsBtn(mRemoteVisors)

		mVPN = systray.AddMenuItem("VPN UI", "VPN UI")
		mPTY = systray.AddMenuItem("DMSGPTY UI", "DMSGPTY UI")
		mStart = systray.AddMenuItem("Start", "Start")
		mAutoconfig = systray.AddMenuItem("Autoconfig", "Autoconfig")
		mShutdown = systray.AddMenuItem("Shutdown", "Shutdown")

		if visor != "" {
			ToggleOff()
		} else {
			ToggleOn()
		}
		systray.AddSeparator()
		//this blank item retains minimum text displacement
		systray.AddMenuItem("                              ", "")
		for {
			select {
			case <-mHV.ClickedCh:
				_, err = script.Exec(skywirecli + ` hv ui`).Stdout()
				if err != nil {
					l.WithError(err).Warn("Failed to open hypervisor UI")
				}
			case <-mVPN.ClickedCh:
				_, err = script.Exec(skywirecli + ` hv vpn ui`).Stdout()
				if err != nil {
					l.WithError(err).Warn("Failed to open VPN UI")
				}
			case <-mPTY.ClickedCh:
				_, err = script.Exec(skywirecli + ` hv dmsg ui`).Stdout()
				if err != nil {
					l.WithError(err).Warn("Failed to open dmsgpty UI")
				}
			case <-mStart.ClickedCh:
				_, err = script.Exec(`sudo systemctl enable --now skywire`).Stdout()
				if err != nil {
					l.WithError(err).Warn("Failed to start skywire")
				} else {
					ToggleOn()
				}
			case <-mAutoconfig.ClickedCh:
				//execute the skywire-autoconfig script includedwith the skywire package
				_, err = script.Exec(`exo-open --launch TerminalEmulator bash -c 'sudo skywire-autoconfig'`).Stdout()
				if err != nil {
					l.WithError(err).Warn("Failed to generate skywire configuration")
				} else {
					ToggleOn()
				}
			case <-mShutdown.ClickedCh:
				if skyenv.OS == "linux" {
					_, _ = script.Exec(`sudo systemctl disable --now skywire skywire-visor`).Stdout() //nolint:errcheck
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
	contents, err = iconFS.ReadFile(gui.IconName)
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
					chn <- index
					l.Info("clicked")
				}
			}
		}(btnChannel, remotevisor, index)
	}
	//TODO
	/*
		_, err = script.Exec(skywirecli + ` hv dmsg ui -v ` + pk).Stdout()
		if err != nil {
			l.WithError(err).Warn("Failed to open dmsgpty UI")
		}
	*/
}

func serversBtn() { //nolint
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
		pk := cipher.PubKey{}
		if err := pk.UnmarshalText([]byte(serverPK)); err != nil {
			continue
		}
		_, err = script.Exec(skywirecli + ` visor app stop` + skyenv.VPNClientName).Stdout()
		if err != nil {
			l.WithError(err).Warn("Failed to stop vpn-client")
		}
		_, err = script.Exec(skywirecli + ` visor app start` + skyenv.VPNClientName + ` ` + pk.String()).Stdout()
		if err != nil {
			l.WithError(err).Warn("Failed to start vpn-client")
		}
	}
}

func handleVPNButton() { //nolint
	appstate, err := script.Exec(skywirecli + ` visor app ls`).Match(skyenv.VPNClientName).Match("stopped").String()
	if err != nil {
		l.WithError(err).Warn("Failed to get vpn-client status")
	}
	if appstate == "" {
		_, err = script.Exec(skywirecli + ` visor app stop` + skyenv.VPNClientName).Stdout()
		if err != nil {
			l.WithError(err).Warn("Failed to stop vpn-client")
		}
	} else {
		_, err = script.Exec(skywirecli + ` visor app start` + skyenv.VPNClientName).Stdout()
		if err != nil {
			l.WithError(err).Warn("Failed to start vpn-client")
		}
	}
}

// ToggleOn when skywire visor is running to show the main menu
func ToggleOn() {
	//check for connected visors
	visors, err := script.Exec(skywirecli + ` dmsgpty list`).String()
	if err != nil {
		l.WithError(err).Warn("Failed to fetch connected visors " + visors)
	}
	mHV.Show()
	if (visors != "") && (visors != "\n") {
		mVisors.Show()
	} else {
		mVisors.Hide()
	}
	mVPN.Show()
	mPTY.Show()
	mShutdown.Show()
	mStart.Hide()
	mAutoconfig.Hide()
	mQuit.Show()
}

// ToggleOff when skywire visor is NOT running to show the start menu
func ToggleOff() {
	mHV.Hide()
	mVisors.Hide()
	mVPN.Hide()
	mPTY.Hide()
	mShutdown.Hide()
	mStart.Show()
	if skyenv.OS == "linux" {
		mAutoconfig.Show()
	}
	mQuit.Show()
}
