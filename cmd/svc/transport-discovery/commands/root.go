// Package commands cmd/svc/transport-discovery/commands/root.go c4-net-discovery
//
// Cobra entry point for the standalone `skywire svc tpd` binary.
// All run logic lives in pkg/services/tpd — this file is just the
// flag set, the optional --config file overlay, and the
// signal-context wiring.
package commands

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/0magnet/calvin"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/services/tpd"
	"github.com/skycoin/skywire/pkg/transport"
)

var (
	flags          services.Flags
	whitelistKeys  string
	dmsgServerType string
	dmsgDisc       = deployment.Prod.DmsgDiscovery
	storeDataPath  string
	uptimeDB       string
	chartsAddr     string
)

func init() {
	flags.Bind(RootCmd.Flags(), services.FlagDefaults{Gen: "tpd", Addr: ":9091", Tag: "transport_discovery", EntryTimeout: tpd.DefaultEntryTimeout})
	RootCmd.Flags().StringVar(&dmsgDisc, "dmsg-disc", dmsgDisc, "url of dmsg-discovery")
	RootCmd.Flags().StringVar(&dmsgServerType, "dmsg-server-type", "", "type of dmsg server on dmsghttp handler")
	RootCmd.Flags().StringVar(&whitelistKeys, "whitelist-keys", "", "network-monitor keys allowed to deregister entries, comma-separated")
	RootCmd.Flags().StringVar(&storeDataPath, "store-data-path", tpd.DefaultStoreDataPath, "path for bandwidth backup files")
	RootCmd.Flags().StringVar(&uptimeDB, "uptime-db", tpd.DefaultUptimeDB, "path for the service-self uptime bbolt store (empty disables)")
	RootCmd.Flags().StringVar(&chartsAddr, "charts-addr", "", "serve only the charts page over plain HTTP on this address")
}

// generateExamples creates example responses from actual struct types
func generateExamples() string {
	pk1 := "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"
	pk2 := "03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"
	tpID := "e7a7f1b3c04047f89e12a0a1459b3456"
	sig := "00000000...00000000"

	return fmt.Sprintf(`
Request/Response Examples:

GET /health
  %s

GET /all-transports?selfTransports=hide
  %s

GET /all-transports/stats
  %s

GET /all-transports/per-key-stats
  %s

GET /transports/id:{id} (auth)
  %s

GET /transports/edge:{pk} (auth)
  [<signed_entry>, ...]

GET /transports/stats/{edge}
  %s

POST /transports/ (auth)
  Request:  %s
  Response: <same with registered timestamp>

DEL /transports/id:{id} (auth)
  Response: "transport deleted"

DEL /transports/deregister (NM auth headers: NM-PK, NM-Sign)
  Request:  %s
  Response: 200 OK

GET /bandwidth/transport/{id}?period=daily&limit=7
  %s

GET /bandwidth/visor/{pk}?period=daily&limit=7
  %s

GET /uptimes
  %s

GET /security/nonces/{pk}
  %s`,
		cmdutil.ExampleJSON(map[string]interface{}{
			"build_info": map[string]string{"version": "v1.3.29"}, "started_at": "2024-01-15T10:00:00Z",
			"dmsg_address": pk1 + ":80", "dmsg_servers": []string{pk2},
		}),
		cmdutil.ExampleJSON([]map[string]interface{}{{
			"entry":      map[string]interface{}{"t_id": tpID, "edges": []string{pk1, pk2}, "type": "stcpr"},
			"signatures": []string{sig, sig}, "registered": 1705312800, "latency_ms": 45.2,
		}}),
		cmdutil.ExampleJSON(map[string]interface{}{
			"total_transports": 150, "by_type": map[string]int{"stcpr": 100, "sudph": 50}, "unique_visors": 75,
		}),
		cmdutil.ExampleJSON(map[string]map[string]int{pk1: {"total": 5, "stcpr": 3, "sudph": 2}}),
		cmdutil.ExampleJSON(map[string]interface{}{
			"entry":      map[string]interface{}{"t_id": tpID, "edges": []string{pk1, pk2}, "type": "stcpr"},
			"signatures": []string{sig, sig}, "registered": 1705312800,
		}),
		cmdutil.ExampleJSON(map[string]interface{}{"total": 5, "by_type": map[string]int{"stcpr": 3, "sudph": 2}}),
		cmdutil.ExampleJSON([]map[string]interface{}{{
			"entry":      map[string]interface{}{"t_id": tpID, "edges": []string{pk1, pk2}, "type": "stcpr"},
			"signatures": []string{sig, sig},
		}}),
		cmdutil.ExampleJSON([]string{tpID}),
		cmdutil.ExampleJSON([]transport.BandwidthData{{SentBytes: 1073741824, RecvBytes: 2147483648}}),
		cmdutil.ExampleJSON(transport.BandwidthData{SentBytes: 5368709120, RecvBytes: 10737418240}),
		cmdutil.ExampleJSON([]map[string]interface{}{{"pk": pk1, "on": true, "tp_count": 5}}),
		cmdutil.ExampleJSON(map[string]interface{}{"nonce": 12345}),
	)
}

// RootCmd contains the root command
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "Transport Discovery Server for skywire",
	Long: calvin.AsciiFont("transport-discovery") + `
Transport Discovery Server - registers and tracks transports between visors.

Depends: redis

Production: ` + deployment.Prod.TransportDiscovery + `
            ` + dmsg.Prod.TransportDiscovery + `
Test:       ` + deployment.Test.TransportDiscovery + `
            ` + dmsg.Test.TransportDiscovery + `

HTTP Endpoints:
  GET  /health                        Health check
  GET  /all-transports                All registered transports
  GET  /all-transports/stats          Transport statistics
  GET  /all-transports/per-key-stats  Transport counts per public key
  GET  /transports/id:{id}            Transport by ID (auth)
  GET  /transports/edge:{edge}        Transports by edge public key (auth)
  GET  /transports/stats/{edge}       Transport stats for edge
  POST /transports/                   Register transport (auth)
  DEL  /transports/id:{id}            Delete transport (auth)
  DEL  /transports/deregister         Deregister transport
  GET  /bandwidth/transport/{id}      Bandwidth for transport
  GET  /bandwidth/visor/{pk}          Bandwidth for visor
  GET  /uptimes                       Visor uptimes (proxied from UT)
  GET  /security/nonces/{pk}          Get nonce for signing
` + generateExamples() + `

Example:
  skywire cli config gen-keys | tee tpd-keys.txt
  transport-discovery --sk $(tail -n1 tpd-keys.txt)`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {
		if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}
		logger := logging.MustGetLogger(flags.LogTag("transport_discovery"))

		cfg, err := buildConfig()
		if err != nil {
			logger.WithError(err).Fatal("failed to build transport-discovery config")
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()
		if err := tpd.New(cfg, logger).Run(ctx); err != nil {
			logger.WithError(err).Fatal("transport-discovery: run failed")
		}
	},
}

// buildConfig collects flag values + the optional --config file
// into one tpd.Config. File values override flag values where set.
func buildConfig() (*tpd.Config, error) {
	common, err := flags.Resolve()
	if err != nil {
		return nil, err
	}
	cfg := &tpd.Config{
		Common:        common,
		Whitelist:     cmdutil.CommaSplit(whitelistKeys),
		StoreDataPath: storeDataPath,
		UptimeDB:      uptimeDB,
		ChartsAddr:    chartsAddr,
		Dmsg: cmdutil.DmsgConfig{
			Discovery:  dmsgDisc,
			ServerType: dmsgServerType,
		},
	}
	if flags.ConfigPath != "" {
		fileCfg, err := tpd.LoadFile(flags.ConfigPath)
		if err != nil {
			return nil, err
		}
		mergeFile(cfg, fileCfg)
	}
	return cfg, nil
}

func mergeFile(dst, src *tpd.Config) {
	services.MergeCommon(&dst.Common, src.Common)
	if len(src.Whitelist) > 0 {
		dst.Whitelist = src.Whitelist
	}
	if src.StoreDataPath != "" {
		dst.StoreDataPath = src.StoreDataPath
	}
	if src.UptimeDB != "" {
		dst.UptimeDB = src.UptimeDB
	}
	if src.ChartsAddr != "" {
		dst.ChartsAddr = src.ChartsAddr
	}
	dst.Dmsg.Merge(src.Dmsg)
}

// Execute executes root CLI command.
func Execute() {
	cmdutil.RunRoot(RootCmd)
}
