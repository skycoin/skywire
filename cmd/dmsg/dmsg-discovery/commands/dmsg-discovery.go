// Package commands cmd/dmsg-discovery/commands/dmsg-discovery.go
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
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	dmsgcmdutil "github.com/skycoin/skywire/pkg/dmsg/cmdutil"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/services/dmsgdisc"
)

var (
	sf                cmdutil.ServiceFlags
	configPath        string
	addr              string
	redisURL          string
	whitelistKeys     string
	entryTimeout      time.Duration
	testMode          bool
	enableLoadTesting bool
	testEnvironment   bool
	sk                cipher.SecKey
	keyFile           string
	dmsgPort          uint16
	authPassphrase    string
	officialServers   string
	dmsgServerType    string
	pprofMode         string
	pprofAddr         string
	mode              string
	enableCXO         bool
)

func init() {
	sf.Init(RootCmd, "dmsg_disc", "")

	RootCmd.Flags().StringVarP(&configPath, "config", "c", "", "path to JSON config file. When set, every other CLI flag below is ignored — fields come from the config file. Generate one with `skywire cli config gen --dmsgdisc -o /etc/skywire/dmsg-discovery.json`.\n\r")
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9090", "address to bind to\n\r")
	RootCmd.Flags().StringVar(&pprofMode, "pprofmode", "", "[ cpu | mem | mutex | block | trace | http ]")
	RootCmd.Flags().StringVar(&pprofAddr, "pprofaddr", "localhost:6060", "pprof http port")
	RootCmd.Flags().StringVar(&authPassphrase, "auth", "", "auth passphrase as simple auth for official dmsg servers registration")
	RootCmd.Flags().StringVar(&officialServers, "official-servers", "", "list of official dmsg servers keys separated by comma")
	RootCmd.Flags().StringVar(&redisURL, "redis", "redis://localhost:6379", "connections string for a redis store\n\r")
	RootCmd.Flags().StringVar(&whitelistKeys, "whitelist-keys", "", "list of whitelisted keys of network monitor used for deregistration")
	// 60m is 12× the client refresh interval (DefaultUpdateInterval*5 = 5m),
	// giving ~2-3 missed refreshes of slack before Redis prunes an entry.
	// Set to 0 to disable expiration (legacy behavior, stale entries
	// will accumulate forever).
	RootCmd.Flags().DurationVar(&entryTimeout, "entry-timeout", 60*time.Minute, "client discovery entry TTL (0 to disable)\n\r")
	RootCmd.Flags().BoolVarP(&testMode, "test-mode", "t", false, "in testing mode")
	RootCmd.Flags().BoolVar(&enableLoadTesting, "enable-load-testing", false, "enable load testing")
	RootCmd.Flags().BoolVar(&testEnvironment, "test-environment", false, "distinguished between prod and test environment")
	RootCmd.Flags().Var(&sk, "sk", "dmsg secret key\n\r")
	RootCmd.Flags().StringVar(&keyFile, "keyfile", "", "path to file containing secret key (auto-generated if missing)\n\r")
	RootCmd.Flags().Uint16Var(&dmsgPort, "dmsgPort", dmsg.DefaultDmsgHTTPPort, "dmsg port value\n\r")
	RootCmd.Flags().StringVar(&dmsgServerType, "dmsg-server-type", "", "type of dmsg server on dmsghttp handler")
	// dmsg-discovery cannot run in dmsg-only mode because dmsg-servers
	// themselves register with it over plain HTTP.
	RootCmd.Flags().StringVar(&mode, "mode", "", "listener mode: http|dual (dmsg-only is rejected — dmsg-servers reach this service over HTTP)")
	RootCmd.Flags().BoolVar(&enableCXO, "cxo", false, "enable CXO clients-by-server publisher feed over DMSG (requires --sk and --mode=dual)")
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
	cfg := &dmsgdisc.Config{
		Addr:              addr,
		Redis:             redisURL,
		DmsgPort:          dmsgPort,
		EntryTimeout:      services.Duration(entryTimeout),
		Mode:              mode,
		AuthPassphrase:    authPassphrase,
		OfficialServers:   commaSplit(officialServers),
		DmsgServerType:    dmsgServerType,
		TestMode:          testMode,
		EnableLoadTesting: enableLoadTesting,
		TestEnvironment:   testEnvironment,
		Whitelist:         commaSplit(whitelistKeys),
		MetricsAddr:       sf.MetricsAddr,
		PProfMode:         pprofMode,
		PProfAddr:         pprofAddr,
		EnableCXO:         enableCXO,
	}

	if keyFile != "" {
		if err := loadOrGenerateKey(keyFile); err != nil {
			return nil, err
		}
	}
	if !sk.Null() {
		cfg.SecKey = sk
	}

	if configPath != "" {
		fileCfg, err := dmsgdisc.LoadFile(configPath)
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
	if src.SecKey != (cipher.SecKey{}) {
		dst.SecKey = src.SecKey
	}
	if src.Addr != "" {
		dst.Addr = src.Addr
	}
	if src.Redis != "" {
		dst.Redis = src.Redis
	}
	if src.DmsgPort != 0 {
		dst.DmsgPort = src.DmsgPort
	}
	if src.EntryTimeout != 0 {
		dst.EntryTimeout = src.EntryTimeout
	}
	if src.Mode != "" {
		dst.Mode = src.Mode
	}
	if src.AuthPassphrase != "" {
		dst.AuthPassphrase = src.AuthPassphrase
	}
	if len(src.OfficialServers) > 0 {
		dst.OfficialServers = src.OfficialServers
	}
	if src.DmsgServerType != "" {
		dst.DmsgServerType = src.DmsgServerType
	}
	if src.TestMode {
		dst.TestMode = true
	}
	if src.EnableLoadTesting {
		dst.EnableLoadTesting = true
	}
	if src.TestEnvironment {
		dst.TestEnvironment = true
	}
	if len(src.Whitelist) > 0 {
		dst.Whitelist = src.Whitelist
	}
	if len(src.DmsgServers) > 0 {
		dst.DmsgServers = src.DmsgServers
	}
	if src.EnableCXO {
		dst.EnableCXO = true
	}
}

func commaSplit(s string) []string {
	if s == "" {
		return nil
	}
	out := make([]string, 0, 4)
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func loadOrGenerateKey(path string) error {
	return dmsgcmdutil.LoadOrGenerateKey(path, &sk)
}

// Execute executes root CLI command.
func Execute() {
	dmsgclient.Execute(RootCmd)
}
