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

var testing bool

func init() {
	//disable sorting, flags appear in the order shown here
	rootCmd.Flags().SortFlags = false
	rootCmd.Flags().BoolVarP(&testing, "test", "t", false, "'go run' external commands from the skywire sources")
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
		systray.SetTemplateIcon(sysTrayIcon, sysTrayIcon)
		systray.SetTitle("Skywire")
		systray.SetTooltip("skywire")
		mHV := systray.AddMenuItem("Hypervisor", "Hypervisor")
		mVPN := systray.AddMenuItem("VPN UI", "VPN UI")
		mPTY := systray.AddMenuItem("DMSGPTY UI", "DMSGPTY UI")
		mShutdown := systray.AddMenuItem("Shutdown", "Shutdown")
		systray.AddSeparator()
		systray.AddMenuItem("", "")
		var err error
		for {
			select {
			case <-mHV.ClickedCh:
				if !testing {
					_, err = script.Exec(`skywire-cli hv ui`).Stdout()
				} else {
					_, err = script.Exec(`go run cmd/skywire-cli/skywire-cli.go hv ui`).Stdout()
				}
				if err != nil {
					l.WithError(err).Fatalln("Failed to open hypervisor UI")
				}
			case <-mVPN.ClickedCh:
				if !testing {
					_, err = script.Exec(`skywire-cli hv vpn ui`).Stdout()
				} else {
					_, err = script.Exec(`go run cmd/skywire-cli/skywire-cli.go hv vpn ui`).Stdout()
				}
				if err != nil {
					l.WithError(err).Fatalln("Failed to open VPN UI")
				}
			case <-mPTY.ClickedCh:
				if !testing {
					_, err = script.Exec(`skywire-cli hv dmsg ui`).Stdout()
				} else {
					_, err = script.Exec(`go run cmd/skywire-cli/skywire-cli.go hv dmsg ui`).Stdout()
				}
				if err != nil {
					l.WithError(err).Fatalln("Failed to open dmsgpty UI")
				}
			case <-mShutdown.ClickedCh:
				if !testing {
					_, err = script.Exec(`skywire-cli visor halt`).Stdout()
				} else {
					_, err = script.Exec(`go run cmd/skywire-cli/skywire-cli.go visor halt`).Stdout()
				}
				if err != nil {
					l.WithError(err).Fatalln("Failed to stop skywire")
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
