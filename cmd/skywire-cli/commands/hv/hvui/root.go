package hvui

import (
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/spf13/cobra"
	"github.com/toqueteos/webbrowser"
)

var logger = logging.MustGetLogger("skywire-cli:launch-browser")

// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "ui",
	Short: "hypervisor UI",
	Run: func(_ *cobra.Command, _ []string) {
		//TODO: get the actual port from config instead of using default value here
		if err := webbrowser.Open("http://127.0.0.1:8000/"); err != nil {
			logger.Fatal("Failed to open hypervisor UI in browser:", err)
		}
	},
}

//TODO: add rpc endpoint to query config path!
/*
// runBrowser opens the hypervisor interface in the browser
func runBrowser(conf *visorconfig.V1) {

	if conf.Hypervisor == nil {
		logger.Errorln("Hypervisor not started - cannot start browser with a regular visor")
		return
	}
	addr := conf.Hypervisor.HTTPAddr
	if addr[0] == ':' {
		addr = "localhost" + addr
	}
	if addr[:4] != "http" {
		if conf.Hypervisor.EnableTLS {
			addr = "https://" + addr
		} else {
			addr = "http://" + addr
		}
	}
	go func() {
		if !isHvRunning(addr, 5) {
			logger.Error("Cannot open hypervisor in browser: status check failed")
			return
		}
		if err := webbrowser.Open(addr); err != nil {
			logger.WithError(err).Error("webbrowser.Open failed")
		}
	}()
}

func isHvRunning(addr string, retries int) bool {
	url := addr + "/api/ping"
	for i := 0; i < retries; i++ {
		time.Sleep(500 * time.Millisecond)
		resp, err := http.Get(url) // nolint: gosec
		if err != nil {
			continue
		}
		err = resp.Body.Close()
		if err != nil {
			continue
		}
		if resp.StatusCode < 400 {
			return true
		}
	}
	return false
}
*/
