/*
skywire systray
*/
package main

import (
	"embed"
	"fmt"
	"log"
	"time"

	"github.com/bitfield/script"
	cc "github.com/ivanpirog/coloredcobra"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/skycoin/systray"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/internal/gui"
	"github.com/skycoin/skywire/pkg/skyenv"
)

var (
	sourcerun    bool
	remotevisors bool
	mHV          *systray.MenuItem
	mVisors      *systray.MenuItem
	mVPN         *systray.MenuItem
	mVPNmenu     *systray.MenuItem
	mPTY         *systray.MenuItem
	mShutdown    *systray.MenuItem
	mStart       *systray.MenuItem
	mAutoconfig  *systray.MenuItem
	mQuit        *systray.MenuItem
)

func init() {
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
	if err := rootCmd.Execute(); err != nil {
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
		var err error
		var skywirecli string
		//skywire-cli command to use
		if !sourcerun {
			skywirecli = "skywire-cli"
		} else {
			skywirecli = "go run cmd/skywire-cli/skywire-cli.go"
		}
		//check that the visor is running and responds over RPC
		visor, err := script.Exec(skywirecli + ` visor pk`).Match("FATAL").String()
		if err != nil {
			l.WithError(err).Warn("Failed to get visor public key")
			//visor should be empty string if the visor is running
			visor = " "
		}
		//check for connected visors
		visors, err := script.Exec(skywirecli + ` dmsgpty list`).String()
		if err != nil {
			l.WithError(err).Warn("Failed to fetch connected visors " + visors)
		}
		if (visors == "") || (visors == "\n") {
			remotevisors = true
		}

		systray.SetTemplateIcon(sysTrayIcon, sysTrayIcon)
		systray.SetTitle("Skywire")

		mHV = systray.AddMenuItem("Hypervisor", "Hypervisor")
		mVisors = systray.AddMenuItemCheckbox("Show Visors", "Show Visors", false)
		mVPN = systray.AddMenuItem("VPN UI", "VPN UI")
		mVPNmenu = systray.AddMenuItemCheckbox("Show VPN Menu", "Show VPN Menu", false)
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
			case <-mVisors.ClickedCh:
				if mVisors.Checked() {
					mVisors.Uncheck()
					ToggleOn()
				} else {
					mVisors.Check()
					AllOff()
					mVisors.Show()
				}
			case <-mVPN.ClickedCh:
				_, err = script.Exec(skywirecli + ` hv vpn ui`).Stdout()
				if err != nil {
					l.WithError(err).Warn("Failed to open VPN UI")
				}
			case <-mVPNmenu.ClickedCh:
				if mVPNmenu.Checked() {
					mVPNmenu.Uncheck()
					ToggleOn()
				} else {
					mVPNmenu.Check()
					AllOff()
					mVPNmenu.Show()
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
					_, _ = script.Exec(`exo-open --launch TerminalEmulator bash -c 'sudo systemctl disable --now skywire skywire-visor'`).Stdout() //nolint:errcheck
					ToggleOff()
					break
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

// ToggleOn when skywire visor is running to show the main menu
func ToggleOn() {
	mHV.Show()
	if remotevisors {
		mVisors.Show()
	}
	mVPN.Show()
	mVPNmenu.Show()
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
	mVPNmenu.Hide()
	mPTY.Hide()
	mShutdown.Hide()
	mStart.Show()
	if skyenv.OS == "linux" {
		mAutoconfig.Show()
	}
	mQuit.Show()
}

/*
// AllOn Show all menu items ; then selectively disable the desired ones
func AllOn() {
	ToggleOn()
	mStart.Show()
	if skyenv.OS == "linux" {
		mAutoconfig.Show()
	}
}
*/

// AllOff Hide all menu items ; then selectively enable the desired ones
func AllOff() {
	ToggleOff()
	mStart.Hide()
	mAutoconfig.Hide()
	mQuit.Hide()
}
