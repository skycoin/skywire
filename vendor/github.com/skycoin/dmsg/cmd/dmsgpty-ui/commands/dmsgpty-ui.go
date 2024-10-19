// Package commands cmd/dmsgpty-ui/commands/root.go
package commands

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/spf13/cobra"

	"github.com/skycoin/dmsg/pkg/dmsgpty"
)

var (
	hostNet  = dmsgpty.DefaultCLINet
	hostAddr = dmsgpty.DefaultCLIAddr()
	addr     = ":8080"
	conf     = dmsgpty.DefaultUIConfig()
)

func init() {
	RootCmd.Flags().StringVar(&hostNet, "hnet", hostNet, "dmsgpty host network name")
	RootCmd.Flags().StringVar(&hostAddr, "haddr", hostAddr, "dmsgpty host network address")
	RootCmd.Flags().StringVar(&addr, "addr", addr, "network address to serve UI on")
	RootCmd.Flags().StringVar(&conf.CmdName, "cmd", conf.CmdName, "command to run when initiating pty")
	RootCmd.Flags().StringArrayVar(&conf.CmdArgs, "arg", conf.CmdArgs, "command arguments to include when initiating pty")
}

// RootCmd contains commands to start a dmsgpty-ui server for a dmsgpty-host
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "DMSG pseudoterminal GUI",
	Long: `
	┌┬┐┌┬┐┌─┐┌─┐┌─┐┌┬┐┬ ┬   ┬ ┬┬
	 │││││└─┐│ ┬├─┘ │ └┬┘───│ ││
	─┴┘┴ ┴└─┘└─┘┴   ┴  ┴    └─┘┴
  ` + "DMSG pseudoterminal GUI",
	Run: func(_ *cobra.Command, _ []string) {
		if _, err := buildinfo.Get().WriteTo(log.Writer()); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}

		ui := dmsgpty.NewUI(dmsgpty.NetUIDialer(hostNet, hostAddr), conf)
		logrus.
			WithField("addr", addr).
			Info("Serving.")

		srv := &http.Server{
			ReadTimeout:       3 * time.Second,
			WriteTimeout:      3 * time.Second,
			IdleTimeout:       30 * time.Second,
			ReadHeaderTimeout: 3 * time.Second,
			Addr:              addr,
			Handler:           ui.Handler(nil),
		}

		err := srv.ListenAndServe()
		logrus.
			WithError(err).
			Info("Stopped serving.")
	},
}

// Execute executes the root command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
