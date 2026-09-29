// Package commands cmd/svc/address-resolver/commands/root.go c4-net-discovery
package commands

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/0magnet/calvin"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/deployment/ar/api"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/services/ar"
)

var (
	flags          services.Flags
	udpAddr        string
	publicUDPAddr  string
	dmsgDisc       string
	whitelistKeys  string
	dmsgServerType string
)

func generateExamples() string {
	pk1 := "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"
	pk2 := "03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"

	return fmt.Sprintf(`
Request/Response Examples:

GET /health
  %s

POST /bind/stcpr (auth)
  Request:  %s
  Response: 200 OK

DEL /bind/stcpr (auth)
  Response: 200 OK

GET /resolve/stcpr/{pk}
  %s

GET /resolve/sudph/{pk}
  %s

GET /transports
  %s

DEL /deregister/{network} (NM auth headers: NM-PK, NM-Sign)
  Request:  %s
  Response: 200 OK

GET /security/nonces/{pk}
  %s`,
		cmdutil.ExampleJSON(map[string]interface{}{
			"build_info":   map[string]string{"version": "v1.3.29"},
			"started_at":   "2024-01-15T10:00:00Z",
			"dmsg_address": pk1 + ":80",
			"dmsg_servers": []string{pk2},
		}),
		cmdutil.ExampleJSON(map[string]interface{}{"port": 30178}),
		cmdutil.ExampleJSON(map[string]string{"addr": "192.168.1.100:30178"}),
		cmdutil.ExampleJSON(map[string]interface{}{
			"addr":      "192.168.1.100:30178",
			"handshake": "<base64_handshake_data>",
		}),
		cmdutil.ExampleJSON(api.ArData{Sudph: []string{pk1}, Stcpr: []string{pk1, pk2}}),
		cmdutil.ExampleJSON([]string{pk1, pk2}),
		cmdutil.ExampleJSON(map[string]interface{}{"nonce": 12345}),
	)
}

func init() {
	flags.Bind(RootCmd.Flags(), services.FlagDefaults{Gen: "ar", Addr: ":9093", Tag: "address_resolver", EntryTimeout: 5 * time.Minute})
	RootCmd.Flags().StringVar(&dmsgDisc, "dmsg-disc", dmsg.DiscURL(false), "url of dmsg-discovery")
	RootCmd.Flags().StringVar(&dmsgServerType, "dmsg-server-type", "", "type of dmsg server on dmsghttp handler")
	RootCmd.Flags().StringVar(&whitelistKeys, "whitelist-keys", "", "network-monitor keys allowed to deregister entries, comma-separated")
	RootCmd.Flags().StringVar(&udpAddr, "udp-addr", ":30178", "UDP address to bind to for SUDPH")
	RootCmd.Flags().StringVar(&publicUDPAddr, "public-udp-address", "", "externally-reachable host:port advertised in /health for SUDPH\n\rrequired for visors that reach this AR over dmsghttp")
}

// RootCmd contains the root command
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "Address Resolver Server for skywire",
	Long: calvin.AsciiFont("address-resolver") + `
Address Resolver Server - resolves visor addresses for STCPR/SUDPH connections.

Depends: redis

Production: ` + deployment.Prod.AddressResolver + `
            ` + dmsg.Prod.AddressResolver + `
Test:       ` + deployment.Test.AddressResolver + `
            ` + dmsg.Test.AddressResolver + `

HTTP Endpoints:
  GET  /health                  Health check
  POST /bind/stcpr              Bind STCPR address (auth)
  DEL  /bind/stcpr              Unbind STCPR address (auth)
  GET  /resolve/{type}/{pk}     Resolve address by type and PK
  GET  /transports              List transports
  DEL  /deregister/{network}    Deregister from network
  GET  /security/nonces/{pk}    Get nonce for signing
` + generateExamples() + `

Note: the specified UDP port must be accessible from the internet for SUDPH.

Example:
  skywire cli config gen-keys > ar-config.json
  skywire svc ar --addr ":9093" --redis "redis://localhost:6379" --sk $(tail -n1 ar-config.json)`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {
		if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}
		logger := logging.MustGetLogger(flags.LogTag("address_resolver"))

		cfg, err := buildConfig()
		if err != nil {
			logger.WithError(err).Fatal("failed to build address-resolver config")
		}
		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()
		if err := ar.New(cfg, logger).Run(ctx); err != nil {
			logger.WithError(err).Fatal("address-resolver: run failed")
		}
	},
}

func buildConfig() (*ar.Config, error) {
	common, err := flags.Resolve()
	if err != nil {
		return nil, err
	}
	cfg := &ar.Config{
		Common:        common,
		UDPAddr:       udpAddr,
		PublicUDPAddr: publicUDPAddr,
		Whitelist:     cmdutil.CommaSplit(whitelistKeys),
		Dmsg: cmdutil.DmsgConfig{
			Discovery:  dmsgDisc,
			ServerType: dmsgServerType,
		},
	}
	if flags.ConfigPath != "" {
		fileCfg, err := ar.LoadFile(flags.ConfigPath)
		if err != nil {
			return nil, err
		}
		mergeFile(cfg, fileCfg)
	}
	return cfg, nil
}

func mergeFile(dst, src *ar.Config) {
	services.MergeCommon(&dst.Common, src.Common)
	if src.UDPAddr != "" {
		dst.UDPAddr = src.UDPAddr
	}
	if src.PublicUDPAddr != "" {
		dst.PublicUDPAddr = src.PublicUDPAddr
	}
	if len(src.Whitelist) > 0 {
		dst.Whitelist = src.Whitelist
	}
	dst.Dmsg.Merge(src.Dmsg)
}

// Execute executes root CLI command
func Execute() {
	cmdutil.RunRoot(RootCmd)
}
