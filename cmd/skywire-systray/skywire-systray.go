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
)

var sourcerun bool

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
	mQuit := systray.AddMenuItem("Quit", "Quit the whole app")
	go func() {
		<-mQuit.ClickedCh
		fmt.Println("Requesting quit")
		systray.Quit()
		fmt.Println("Finished quitting")
	}()

	// We can manipulate the systray in other goroutines
	go func() {
		var err error
		var visor string
		//		var v string
		//		var visors string
		if !sourcerun {
			visor, err = script.Exec(`bash -c 'skywire-cli visor pk | grep FATAL'`).String()
		} else {
			visor, err = script.Exec(`bash -c 'go run cmd/skywire-cli/skywire-cli.go visor pk | grep FATAL'`).String()
		}
		if err != nil {
			l.WithError(err).Warn("Failed to get visor public key")
			visor = ""
		}

		//		if !sourcerun {
		//			visors, err = script.Exec(`skywire-cli dmsgpty list`).String()
		//		} else {
		//			visors, err = script.Exec(`go run cmd/skywire-cli/skywire-cli.go dmsgpty list`).String()
		//		}
		//		if err != nil {
		//			l.WithError(err).Fatalln("Failed to fetch connected visors " +visors)
		//		}

		systray.SetTemplateIcon(sysTrayIcon, sysTrayIcon)
		systray.SetTitle("Skywire")
		//systray.SetTooltip("skywire")

		mHV := systray.AddMenuItem("Hypervisor", "Hypervisor")
		mVPN := systray.AddMenuItem("VPN UI", "VPN UI")
		mVPNmenu := systray.AddMenuItemCheckbox("Show VPN Menu", "Check Me", false)
		mPTY := systray.AddMenuItem("DMSGPTY UI", "DMSGPTY UI")
		mStart := systray.AddMenuItem("Start", "Start")
		mAutoconfig := systray.AddMenuItem("Autoconfig", "Autoconfig")
		mShutdown := systray.AddMenuItem("Shutdown", "Shutdown")
		l.Info("Visor:" + visor + "|")
		if visor != "" {
			mHV.Hide()
			mVPN.Hide()
			mVPNmenu.Hide()
			mPTY.Hide()
			mShutdown.Hide()
		} else {
			mStart.Hide()
			mAutoconfig.Hide()
		}
		systray.AddSeparator()
		systray.AddMenuItem("", "")
		for {
			select {
			case <-mHV.ClickedCh:
				if !sourcerun {
					_, err = script.Exec(`skywire-cli hv ui`).Stdout()
				} else {
					_, err = script.Exec(`go run cmd/skywire-cli/skywire-cli.go hv ui`).Stdout()
				}
				if err != nil {
					l.WithError(err).Fatalln("Failed to open hypervisor UI")
				}
			case <-mVPN.ClickedCh:
				if !sourcerun {
					_, err = script.Exec(`skywire-cli hv vpn ui`).Stdout()
				} else {
					_, err = script.Exec(`go run cmd/skywire-cli/skywire-cli.go hv vpn ui`).Stdout()
				}
				if err != nil {
					l.WithError(err).Fatalln("Failed to open VPN UI")
				}
			case <-mVPNmenu.ClickedCh:
				if mVPNmenu.Checked() {
					mVPNmenu.Uncheck()
					mVPNmenu.SetTitle("Show VPN menu")
				} else {
					mVPNmenu.Check()
					mVPNmenu.SetTitle("Hide VPN menu")
				}
			case <-mPTY.ClickedCh:
				if !sourcerun {
					_, err = script.Exec(`skywire-cli hv dmsg ui`).Stdout()
				} else {
					_, err = script.Exec(`go run cmd/skywire-cli/skywire-cli.go hv dmsg ui`).Stdout()
				}
				if err != nil {
					l.WithError(err).Fatalln("Failed to open dmsgpty UI")
				}
			case <-mStart.ClickedCh:
				if !sourcerun {
					_, err = script.Exec(`exo-open --launch TerminalEmulator bash -c 'sudo systemctl enable --now skywire'`).Stdout()
				} else {
					_, err = script.Exec(`exo-open --launch TerminalEmulator bash -c 'sudo go run cmd/skywire-visor/skywire-visor.go -p'`).Stdout()
				}
				if err != nil {
					l.WithError(err).Fatalln("Failed to start skywire")
				}
				mHV.Show()
				mVPN.Show()
				mVPNmenu.Show()
				mPTY.Show()
				mShutdown.Show()
				mStart.Hide()
				mAutoconfig.Hide()
			case <-mAutoconfig.ClickedCh:
				//execute the skywire-autoconfig script includedwith the skywire package
				_, err = script.Exec(`exo-open --launch TerminalEmulator bash -c 'sudo skywire-autoconfig'`).Stdout()
				if err != nil {
					l.WithError(err).Fatalln("Failed to generate skywire configuration")
				}
				mHV.Show()
				mVPN.Show()
				mVPNmenu.Show()
				mPTY.Show()
				mShutdown.Show()
				mStart.Hide()
				mAutoconfig.Hide()
			case <-mShutdown.ClickedCh:
				_, _ = script.Exec(`exo-open --launch TerminalEmulator bash -c 'sudo systemctl disable --now skywire skywire-visor'`).Stdout() //nolint:errcheck
				if !sourcerun {
					_, err = script.Exec(`skywire-cli visor halt 2> /dev/null`).Stdout()
				} else {
					_, err = script.Exec(`go run cmd/skywire-cli/skywire-cli.go visor halt 2> /dev/null`).Stdout()
				}
				if err != nil {
					l.WithError(err).Warn("Failed to stop skywire")
				}
				mHV.Hide()
				mVPN.Hide()
				mVPNmenu.Hide()
				mPTY.Hide()
				mShutdown.Hide()
				mStart.Show()
				mAutoconfig.Show()
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

/*


func runApp(args ...string) {
	l := logging.NewMasterLogger()
	sysTrayIcon, err := gui.ReadSysTrayIcon()
	if err != nil {
		l.WithError(err).Fatalln("Failed to read system tray icon")
	}

	conf := initConfig(l, confPath)

	go func() {
		runVisor(conf)
		systray.Quit()
	}()

	systray.Run(gui.GetOnGUIReady(sysTrayIcon, conf), gui.OnGUIQuit)

}

func setStopFunction(log *logging.MasterLogger, cancel context.CancelFunc, fn func() error) {
	stopVisorWg.Add(1)
	defer stopVisorWg.Done()

	stopVisorFn = func() {
		if err := fn(); err != nil {
			log.WithError(err).Error("Visor closed with error.")
		}
		cancel()
		stopVisorWg.Wait()
	}

	gui.SetStopVisorFn(func() {
		stopVisorFn()
	})
}

func quitSystray() {
	systray.Quit()
}
*/
