package skywirevisormobile

import (
	"context"
	"fmt"
	_ "net/http/pprof" // nolint:gosec // https://golang.org/doc/diagnostics.html#profiling

	"github.com/SkycoinProject/dmsg/cmdutil"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/SkycoinProject/skywire-mainnet/pkg/visor"

	"github.com/SkycoinProject/skywire-mainnet/pkg/restart"
	"github.com/SkycoinProject/skywire-mainnet/pkg/visor/visorconfig"
)

const (
	visorConfig = `{
	"version": "v1.0.0",
	"sk": "c5b5c8b68ce91dd42bf0343926c7b551336c359c8e13a83cedd573d377aacf8c",
	"pk": "0305deabe88b41b25697ee30133e514bd427be208f4590bc85b27cd447b19b1538",
	"dmsg": {
		"discovery": "http://dmsg.discovery.skywire.cc",
		"sessions_count": 1
	},
	"stcp": {
		"pk_table": null,
		"local_address": ":7777"
	},
	"transport": {
		"discovery": "http://transport.discovery.skywire.cc",
		"address_resolver": "http://address.resolver.skywire.cc",
		"log_store": {
			"type": "file",
			"location": "./transport_logs"
		},
		"trusted_visors": null
	},
	"routing": {
		"setup_nodes": [
			"026c5a07de617c5c488195b76e8671bf9e7ee654d0633933e202af9e111ffa358d"
		],
		"route_finder": "http://routefinder.skywire.cc",
		"route_finder_timeout": "10s"
	},
	"uptime_tracker": {
		"addr": "http://uptime.tracker.skywire.cc"
	},
	"launcher": {
		"discovery": {
			"update_interval": "30s",
			"proxy_discovery_addr": "http://service.discovery.skywire.cc"
		},
		"apps": [
			{
				"name": "skychat",
				"args": [
					"-addr",
					":8001"
				],
				"auto_start": true,
				"port": 1
			},
			{
				"name": "skysocks",
				"auto_start": true,
				"port": 3
			},
			{
				"name": "skysocks-client",
				"auto_start": false,
				"port": 13
			},
			{
				"name": "vpn-server",
				"auto_start": false,
				"port": 44
			},
			{
				"name": "vpn-client",
				"auto_start": false,
				"port": 43
			}
		],
		"server_addr": "localhost:5505",
		"bin_path": "./apps",
		"local_path": "./local"
	},
	"hypervisors": [],
	"cli_addr": "localhost:3435",
	"log_level": "info",
	"shutdown_timeout": "10s",
	"restart_check_delay": "1s"
}`
)

var restartCtx = restart.CaptureContext()

func RunVisor() {
	log := logging.NewMasterLogger()

	conf, err := initConfig(log, "./skywire-config.json")
	if err != nil {
		fmt.Printf("Error getting visor config: %v\n", err)
		return
	}

	v, ok := visor.NewVisor(conf, restartCtx)
	if !ok {
		log.Fatal("Failed to start visor.")
	}

	ctx, cancel := cmdutil.SignalContext(context.Background(), log)
	defer cancel()

	// Wait.
	<-ctx.Done()

	if err := v.Close(); err != nil {
		log.WithError(err).Error("Visor closed with error.")
	}
}

func initConfig(mLog *logging.MasterLogger, confPath string) (*visorconfig.V1, error) {
	conf, err := visorconfig.Parse(mLog, confPath, []byte(visorConfig))
	if err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	return conf, nil
}
