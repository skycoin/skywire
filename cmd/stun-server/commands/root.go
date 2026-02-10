// Package commands cmd/stun-server/commands/root.go
package commands

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/skycoin/skywire/pkg/stunserver"
)

var (
	primaryIP   string
	secondaryIP string
	port        int
	altPort     int
	logLvl      string
	tag         string
)

func init() {
	RootCmd.Flags().StringVar(&primaryIP, "primary-ip", "", "primary listening IP (required)")
	RootCmd.Flags().StringVar(&secondaryIP, "secondary-ip", "", "secondary listening IP (required)")
	RootCmd.Flags().IntVar(&port, "port", 3478, "primary STUN port")
	RootCmd.Flags().IntVar(&altPort, "alt-port", 3479, "alternate STUN port")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "info", "[info|error|warn|debug|trace|panic]")
	RootCmd.Flags().StringVar(&tag, "tag", "stun", "logging tag")
}

// RootCmd is the root command for the STUN server.
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "STUN server for skywire",
	Long: calvin.AsciiFont("stun-server") + `

STUN server implementing RFC 3489 NAT discovery.
Requires two distinct IPs for full NAT type detection.

  skywire svc stun --primary-ip 139.162.160.227 --secondary-ip 172.104.247.120
  skywire svc stun --primary-ip 127.0.0.1 --secondary-ip 127.0.0.2 --port 3478 --alt-port 3479`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {
		logger := logging.MustGetLogger(tag)
		lvl, err := logging.LevelFromString(logLvl)
		if err != nil {
			logger.Fatal("Invalid loglvl detected")
		}
		logging.SetLevel(lvl)

		if primaryIP == "" || secondaryIP == "" {
			logger.Fatal("Both --primary-ip and --secondary-ip are required")
		}

		srv := &stunserver.Server{
			PrimaryIP:   primaryIP,
			SecondaryIP: secondaryIP,
			PrimaryPort: port,
			AltPort:     altPort,
			Software:    "skywire-stun",
			Log:         logger,
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

		go func() {
			<-stop
			cancel()
		}()

		if err := srv.Start(ctx); err != nil {
			logger.Fatalf("STUN server failed: %v", err)
		}
	},
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
