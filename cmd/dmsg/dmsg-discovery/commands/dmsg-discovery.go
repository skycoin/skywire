// Package commands cmd/dmsg/dmsg-discovery/commands/dmsg-discovery.go c1-net-dmsg
//
// Cobra entry point for the standalone `skywire dmsg disc` binary.
// All the actual run logic lives in pkg/services/dmsgdisc — this file
// is just the flag set, the optional --config file overlay, and the
// signal-context wiring.
package commands

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/services/dmsgdisc"
)

var (
	flags             services.Flags
	syslogAddr        string
	syslogNet         string
	whitelistKeys     string
	enableLoadTesting bool
	authPassphrase    string
	officialServers   string
	dmsgServerType    string
	chartsAddr        string
)

func init() {
	flags.Bind(RootCmd.Flags(), services.FlagDefaults{Gen: "dmsgdisc", Addr: ":9090", Tag: "dmsg_disc", EntryTimeout: 60 * time.Minute})
	RootCmd.Flags().StringVar(&dmsgServerType, "dmsg-server-type", "", "type of dmsg server on dmsghttp handler")
	RootCmd.Flags().StringVar(&whitelistKeys, "whitelist-keys", "", "network-monitor keys allowed to deregister entries, comma-separated")
	RootCmd.Flags().StringVar(&authPassphrase, "auth", "", "auth passphrase as simple auth for official dmsg servers registration")
	RootCmd.Flags().StringVar(&officialServers, "official-servers", "", "list of official dmsg servers keys separated by comma")
	RootCmd.Flags().BoolVar(&enableLoadTesting, "enable-load-testing", false, "enable load testing")
	RootCmd.Flags().StringVar(&chartsAddr, "charts-addr", "", "serve only the charts page over plain HTTP on this address")
	RootCmd.Flags().StringVar(&syslogAddr, "syslog", "", "address in which to dial to syslog server")
	RootCmd.Flags().StringVar(&syslogNet, "syslog-net", "udp", "network in which to dial to syslog server")
}

// RootCmd contains commands for dmsg-discovery
var RootCmd = &cobra.Command{
	Use:   dmsgclient.ExecName(),
	Short: "DMSG Discovery Server",
	Long: `
	┌┬┐┌┬┐┌─┐┌─┐  ┌┬┐┬┌─┐┌─┐┌─┐┬  ┬┌─┐┬─┐┬ ┬
	 │││││└─┐│ ┬───│││└─┐│  │ │└┐┌┘├┤ ├┬┘└┬┘
	─┴┘┴ ┴└─┘└─┘  ─┴┘┴└─┘└─┘└─┘ └┘ └─┘┴└─ ┴
DMSG Discovery Server - registers and discovers DMSG clients and servers.

Depends: redis

HTTP Endpoints:
  GET  /health                                Health check
  GET  /dmsg-discovery/entry/{pk}             Get entry by public key
  POST /dmsg-discovery/entry/                 Register/update entry
  POST /dmsg-discovery/entry/{pk}             Register/update entry
  DEL  /dmsg-discovery/entry                  Delete entry
  GET  /dmsg-discovery/entries                All entries
  GET  /dmsg-discovery/visorEntries           All visor entries
  DEL  /dmsg-discovery/deregister             Deregister entry
  GET  /dmsg-discovery/available_servers      Available DMSG servers
  GET  /dmsg-discovery/all_servers            All DMSG servers
  GET  /dmsg-discovery/servers/clients        Clients by all servers
  GET  /dmsg-discovery/server/{pk}/clients    Clients by specific server
` + generateExamples() + `

Example:
  skywire cli config gen-keys > dmsgd-config.json
  skywire dmsg disc --sk $(tail -n1 dmsgd-config.json)`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {
		if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}
		sf := cmdutil.ServiceFlags{Syslog: syslogAddr, SyslogNet: syslogNet, LogLevel: flags.LogLevel, Tag: flags.LogTag("dmsg_disc")}
		if err := sf.Check(); err != nil {
			log.Fatal(err)
		}
		logger := sf.Logger()

		cfg, err := buildConfig()
		if err != nil {
			logger.WithError(err).Fatal("failed to build dmsg-discovery config")
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()
		if err := dmsgdisc.New(cfg, logger).Run(ctx); err != nil {
			logger.WithError(err).Fatal("dmsg-discovery: run failed")
		}
	},
}

// buildConfig collects the cobra flag values and the optional
// --config file into a single dmsgdisc.Config. When --config is
// provided, file values take precedence over flag values for fields
// the file sets; flag-only fields (or zero-valued file fields) keep
// the flag default.
func buildConfig() (*dmsgdisc.Config, error) {
	common, err := flags.Resolve()
	if err != nil {
		return nil, err
	}
	cfg := &dmsgdisc.Config{
		Common:            common,
		AuthPassphrase:    authPassphrase,
		OfficialServers:   cmdutil.CommaSplit(officialServers),
		DmsgServerType:    dmsgServerType,
		EnableLoadTesting: enableLoadTesting,
		Whitelist:         cmdutil.CommaSplit(whitelistKeys),
		ChartsAddr:        chartsAddr,
	}

	if flags.ConfigPath != "" {
		fileCfg, err := dmsgdisc.LoadFile(flags.ConfigPath)
		if err != nil {
			return nil, err
		}
		// file values win where set
		mergeFile(cfg, fileCfg)
	}
	return cfg, nil
}

// mergeFile copies non-zero fields from src into dst. Mirrors the
// previous applyConfig logic so behavior is unchanged: a partial
// config file can override individual fields without erasing
// flag-set values for the rest.
func mergeFile(dst, src *dmsgdisc.Config) {
	services.MergeCommon(&dst.Common, src.Common)
	if src.AuthPassphrase != "" {
		dst.AuthPassphrase = src.AuthPassphrase
	}
	if len(src.OfficialServers) > 0 {
		dst.OfficialServers = src.OfficialServers
	}
	if src.DmsgServerType != "" {
		dst.DmsgServerType = src.DmsgServerType
	}
	if src.EnableLoadTesting {
		dst.EnableLoadTesting = true
	}
	if len(src.Whitelist) > 0 {
		dst.Whitelist = src.Whitelist
	}
	if len(src.DmsgServers) > 0 {
		dst.DmsgServers = src.DmsgServers
	}
	if src.ChartsAddr != "" {
		dst.ChartsAddr = src.ChartsAddr
	}
}

// Execute executes root CLI command.
func Execute() {
	dmsgclient.Execute(RootCmd)
}
