/*
skywire systray
*/
package main

import (
	"embed"
	"fmt"
	"time"

	"github.com/bitfield/script"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/skycoin/systray"

	"github.com/skycoin/skywire/internal/gui"
)

//go:embed icons/*
var iconFS embed.FS

func main() {
	onExit := func() {
		now := time.Now()
		fmt.Println("Exit at", now.String())
	}

	systray.Run(onReady, onExit)
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
		for {
			select {
			case <-mHV.ClickedCh:
				_, err := script.Exec(`skywire-cli hv ui`).Stdout()
				if err != nil {
					l.WithError(err).Fatalln("Failed to open hypervisor UI")
				}
			case <-mVPN.ClickedCh:
				_, err := script.Exec(`skywire-cli hv vpn ui`).Stdout()
				if err != nil {
					l.WithError(err).Fatalln("Failed to open VPN UI")
				}
			case <-mPTY.ClickedCh:
				_, err := script.Exec(`skywire-cli hv dmsg ui`).Stdout()
				if err != nil {
					l.WithError(err).Fatalln("Failed to open dmsgpty UI")
				}
			case <-mShutdown.ClickedCh:
				_, err := script.Exec(`skywire-cli visor halt`).Stdout()
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
