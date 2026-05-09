// Package commands cmd/stun-server/commands/root.go
package commands

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services/stun"
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
	RootCmd.Flags().IntVar(&port, "port", 3478, "primary STUN port\n\r")
	RootCmd.Flags().IntVar(&altPort, "alt-port", 3479, "alternate STUN port\n\r")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "info", "[info|error|warn|debug|trace|panic]\n\r")
	RootCmd.Flags().StringVar(&tag, "tag", "stun", "logging tag\n\r")
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
		cfg := &stun.Config{
			PrimaryIP:   primaryIP,
			SecondaryIP: secondaryIP,
			Port:        port,
			AltPort:     altPort,
			LogLevel:    logLvl,
			Tag:         tag,
		}
		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()
		if err := stun.New(cfg, logger).Run(ctx); err != nil {
			logger.WithError(err).Fatal("stun-server: run failed")
		}
	},
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
