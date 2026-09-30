// Package commands cmd/svc/service-discovery/commands/root.go c4-net-discovery
package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/0magnet/calvin"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/services/sd"
)

var log = logging.MustGetLogger("service-discovery")

var (
	flags          services.Flags
	dmsgDisc       string
	whitelistKeys  string
	dmsgServerType string
	geoipURL       string
)

// generateExamples creates example responses from actual struct types
func generateExamples() string {
	pk1 := "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"
	pk2 := "03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"

	serviceExample := map[string]interface{}{
		"address": pk1 + ":3", "type": "vpn", "version": "v1.3.29",
		"geo": map[string]interface{}{"country": "US", "region": "CA", "lat": 37.77, "lon": -122.41},
	}

	return fmt.Sprintf(`
Request/Response Examples:

GET /health
  %s

GET /api/services?type=vpn&version=v1.3&country=US&quantity=10
  %s

GET /api/services/{addr}?type=vpn
  %s

POST /api/services (auth)
  Request:  %s
  Response: (same with geo data added)

DEL /api/services/{addr}?type=vpn (auth)
  Response: true

DEL /api/services/deregister/{type} (NM auth headers: NM-PK, NM-Sign)
  Request:  %s
  Response: true

GET /security/nonces/{pk}
  %s`,
		cmdutil.ExampleJSON(map[string]interface{}{
			"build_info":   map[string]string{"version": "v1.3.29"},
			"started_at":   "2024-01-15T10:00:00Z",
			"dmsg_address": pk1 + ":80",
			"dmsg_servers": []string{pk2},
		}),
		cmdutil.ExampleJSON([]map[string]interface{}{serviceExample}),
		cmdutil.ExampleJSON(serviceExample),
		cmdutil.ExampleJSON(map[string]interface{}{
			"address": pk1 + ":3", "type": "vpn", "version": "v1.3.29",
		}),
		cmdutil.ExampleJSON([]string{pk1, pk2}),
		cmdutil.ExampleJSON(map[string]interface{}{"nonce": 12345}),
	)
}

func init() {
	flags.Bind(RootCmd.Flags(), services.FlagDefaults{Gen: "sd", Addr: ":9098", Tag: "service_discovery", EntryTimeout: 5 * time.Minute})
	RootCmd.Flags().StringVar(&dmsgDisc, "dmsg-disc", dmsg.DiscURL(false), "url of dmsg-discovery")
	RootCmd.Flags().StringVar(&dmsgServerType, "dmsg-server-type", "", "type of dmsg server on dmsghttp handler")
	RootCmd.Flags().StringVar(&whitelistKeys, "whitelist-keys", "", "network-monitor keys allowed to deregister entries, comma-separated")
	RootCmd.Flags().StringVar(&geoipURL, "geoip", deployment.Prod.GeoIP, "url of geoip service")
}

// RootCmd contains the root service-discovery command
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "Service discovery server",
	Long: calvin.AsciiFont("service-discovery") + `
Service Discovery Server - registers and discovers services (VPN, proxy, visor).

Depends: redis

Production: ` + deployment.Prod.ServiceDiscovery + `
            ` + dmsg.Prod.ServiceDiscovery + `
Test:       ` + deployment.Test.ServiceDiscovery + `
            ` + dmsg.Test.ServiceDiscovery + `

HTTP Endpoints:
  GET  /health                           Health check
  GET  /api/services                     List services (?type=proxy|vpn|visor)
  GET  /api/services/{addr}              Get specific service
  POST /api/services                     Register service (auth)
  DEL  /api/services/{addr}              Delete service (auth)
  DEL  /api/services/deregister/{type}   Deregister by type
  GET  /security/nonces/{pk}             Get nonce for signing
` + generateExamples() + `

Example:
  skywire cli config gen-keys | tee sd-keys.txt
  service-discovery --sk $(tail -n1 sd-keys.txt)`,
	Run: func(_ *cobra.Command, _ []string) {
		if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}
		cfg, err := buildConfig()
		if err != nil {
			log.WithError(err).Fatal("failed to build service-discovery config")
		}
		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()
		if err := sd.New(cfg, log).Run(ctx); err != nil {
			log.WithError(err).Fatal("service-discovery: run failed")
		}
	},
}

// buildConfig collects flag values + the optional --config file
// into one sd.Config. File values override flag values where set.
func buildConfig() (*sd.Config, error) {
	common, err := flags.Resolve()
	if err != nil {
		return nil, err
	}
	cfg := &sd.Config{
		Common:    common,
		Whitelist: cmdutil.CommaSplit(whitelistKeys),
		GeoIP:     geoipURL,
		Dmsg: cmdutil.DmsgConfig{
			Discovery:  dmsgDisc,
			ServerType: dmsgServerType,
		},
	}
	if flags.ConfigPath != "" {
		fileCfg, err := sd.LoadFile(flags.ConfigPath)
		if err != nil {
			return nil, err
		}
		mergeFile(cfg, fileCfg)
	}
	return cfg, nil
}

func mergeFile(dst, src *sd.Config) {
	services.MergeCommon(&dst.Common, src.Common)
	if len(src.Whitelist) > 0 {
		dst.Whitelist = src.Whitelist
	}
	if src.GeoIP != "" {
		dst.GeoIP = src.GeoIP
	}
	dst.Dmsg.Merge(src.Dmsg)
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
