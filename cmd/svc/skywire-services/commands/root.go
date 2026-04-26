// Package commands cmd/skywire-services/commands/services.go
package commands

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	ar "github.com/skycoin/skywire/cmd/svc/address-resolver/commands"
	conf "github.com/skycoin/skywire/cmd/svc/conf/commands"
	confbs "github.com/skycoin/skywire/cmd/svc/config-bootstrapper/commands"
	geoip "github.com/skycoin/skywire/cmd/svc/geoip/commands"
	nm "github.com/skycoin/skywire/cmd/svc/network-monitor/commands"
	rf "github.com/skycoin/skywire/cmd/svc/route-finder/commands"
	sd "github.com/skycoin/skywire/cmd/svc/service-discovery/commands"
	sn "github.com/skycoin/skywire/cmd/svc/setup-node/commands"
	stunsvr "github.com/skycoin/skywire/cmd/svc/stun-server/commands"
	se "github.com/skycoin/skywire/cmd/svc/sw-env/commands"
	tpd "github.com/skycoin/skywire/cmd/svc/transport-discovery/commands"
	tps "github.com/skycoin/skywire/cmd/svc/transport-setup/commands"
	ut "github.com/skycoin/skywire/cmd/svc/uptime-tracker/commands"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
)

func init() {
	conf.ServicesConfCmd.AddCommand(
		conf.DmsghttpConfCmd,
		conf.HTTPConfCmd,
	)
	RootCmd.AddCommand(
		tpd.RootCmd,
		tps.RootCmd,
		ar.RootCmd,
		rf.RootCmd,
		confbs.RootCmd,
		conf.ServicesConfCmd,
		se.RootCmd,
		ut.RootCmd,
		sd.RootCmd,
		sn.RootCmd,
		nm.RootCmd,
		geoip.RootCmd,
		stunsvr.RootCmd,
	)
	tpd.RootCmd.Use = "tpd"
	tps.RootCmd.Use = "tps"
	ar.RootCmd.Use = "ar"
	rf.RootCmd.Use = "rf"
	confbs.RootCmd.Use = "confbs"
	conf.RootCmd.Use = "conf"
	se.RootCmd.Use = "se"
	ut.RootCmd.Use = "ut"
	sd.RootCmd.Use = "sd"
	sn.RootCmd.Use = "sn"
	nm.RootCmd.Use = "nm"
	conf.DmsghttpConfCmd.Use = "dmsghttp"
	conf.HTTPConfCmd.Use = "http"
	conf.ServicesConfCmd.Use = "conf"
	geoip.RootCmd.Use = "ip"
	stunsvr.RootCmd.Use = "stun"
}

// RootCmd contains all subcommands
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "Skywire services",
	Long: calvin.AsciiFont("skywire-services") + `
	Skywire services`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
