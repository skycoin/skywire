// Package commands cmd/config-bootstrapper/commands/root.go
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tidwall/pretty"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/config-bootstrapper/api"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/svcmode"
)

var (
	addr           string
	tag            string
	stunPath       string
	domain         string
	dmsgDisc       string
	sk             cipher.SecKey
	keyFile        string
	dmsgPort       uint16
	dmsgServerType string
	pprofAddr      string
	mode           string
)

// exampleJSON marshals v to indented JSON with color, returning empty string on error
func exampleJSON(v interface{}) string {
	b, err := json.MarshalIndent(v, "    ", "  ")
	if err != nil {
		return ""
	}
	return string(pretty.Color(b, nil))
}

// generateExamples creates example responses from actual struct types
func generateExamples() string {
	exPK1 := "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"
	exPK2 := "03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"

	// GET /health - api.HealthCheckResponse
	healthExample := map[string]interface{}{
		"build_info": map[string]interface{}{
			"version": "v1.3.29",
			"commit":  "abc1234",
			"date":    "2024-01-15T10:30:00Z",
		},
		"started_at":   "2024-01-15T10:00:00Z",
		"dmsg_address": exPK1 + ":80",
	}

	// GET / - visorconfig.Services (partial, key fields shown)
	servicesExample := map[string]interface{}{
		"dmsg_discovery":      "http://dmsgd.skywire.skycoin.com",
		"transport_discovery": "http://tpd.skywire.skycoin.com",
		"address_resolver":    "http://ar.skywire.skycoin.com",
		"route_finder":        "http://rf.skywire.skycoin.com",
		"uptime_tracker":      "http://ut.skywire.skycoin.com",
		"service_discovery":   "http://sd.skycoin.com",
		"route_setup_nodes":   []string{exPK1, exPK2},
		"stun_servers":        []string{"stun.l.google.com:19302"},
		"transport_setup":     []string{exPK1},
	}

	return fmt.Sprintf(`
Response Examples (from actual struct types):

GET /health - api.HealthCheckResponse
%s

GET / - visorconfig.Services
%s`,
		exampleJSON(healthExample),
		exampleJSON(servicesExample))
}

func init() {
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9082", "address to bind to\n\r")
	RootCmd.Flags().StringVar(&pprofAddr, "pprof", "", "address to bind pprof debug server (e.g. localhost:6060)")
	RootCmd.Flags().StringVar(&tag, "tag", "address_resolver", "logging tag\n\r")
	RootCmd.Flags().StringVarP(&stunPath, "config", "c", "./config.json", "stun server list file location\n\r")
	RootCmd.Flags().StringVarP(&domain, "domain", "d", "skywire.skycoin.com", "the domain of the endpoints\n\r")
	RootCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "D", dmsg.DiscURL(false), "url of dmsg-discovery\n\r")
	RootCmd.Flags().Var(&sk, "sk", "dmsg secret key\n\r")
	RootCmd.Flags().StringVar(&keyFile, "keyfile", "", "path to file containing secret key (auto-generated if missing)\n\r")
	RootCmd.Flags().Uint16Var(&dmsgPort, "dmsgPort", dmsg.DefaultDmsgHTTPPort, "dmsg port value\n\r")
	RootCmd.Flags().StringVar(&dmsgServerType, "dmsg-server-type", "", "type of dmsg server on dmsghttp handler")
	RootCmd.Flags().StringVar(&mode, "mode", "", "listener mode: http|dmsg|dual (default dual if --sk, else http; env SKYWIRE_SVC_MODE overrides)")
}

// RootCmd contains the root command
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "Config Bootstrap Server for skywire",
	Long: calvin.AsciiFont("config-bootstrapper") + `
Config Bootstrap Server - provides initial configuration for visors.

Production: http://conf.skywire.skycoin.com
Test:       http://conf.skywire.dev

HTTP Endpoints:
  GET  /health     Health check
  GET  /           Bootstrap configuration (services URLs, keys, etc.)
  GET  /dmsghttp   DMSG HTTP configuration
` + generateExamples(),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {
		if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}

		logger := logging.MustGetLogger(tag)

		metricsutil.ServePProf(logger, pprofAddr, "config-bootstrapper")

		config := readConfig(logger, stunPath)

		if keyFile != "" {
			if err := cmdutil.LoadOrGenerateKey(keyFile, &sk); err != nil {
				logger.Fatal("Failed to load keyfile: ", err)
			}
		}
		pk, err := sk.PubKey()
		if err != nil {
			logger.WithError(err).Warn("No SecKey found. Skipping serving on dmsghttp.")
		}

		var dmsgAddr string
		if !pk.Null() {
			dmsgAddr = fmt.Sprintf("%s:%d", pk.Hex(), dmsgPort)
		}

		conAPI := api.New(logger, config, domain, dmsgAddr)

		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()

		resolvedMode, err := svcmode.ResolveMode(mode, !sk.Null())
		if err != nil {
			logger.WithError(err).Fatal("invalid --mode")
		}

		h, err := svcmode.Start(ctx, svcmode.Config{
			Mode:                resolvedMode,
			HTTPAddr:            addr,
			Handler:             conAPI,
			PK:                  pk,
			SK:                  sk,
			DmsgPort:            dmsgPort,
			DmsgDiscovery:       dmsgDisc,
			DmsgServerType:      dmsgServerType,
			EmbeddedDmsgServers: dmsg.Prod.DmsgServers,
			SurveyWhitelist:     deployment.Prod.SurveyWhitelist,
			Log:                 logger,
		})
		if err != nil {
			logger.WithError(err).Fatal("failed to start listeners")
		}
		defer h.Close()

		select {
		case <-ctx.Done():
		case err := <-h.Errors():
			logger.WithError(err).Error("listener failed")
			cancel()
		}

		conAPI.Close()
	},
}

func readConfig(log *logging.Logger, confPath string) (config api.Config) {
	f, err := os.Open(confPath) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			log.WithField("filepath", confPath).
				Info("Config file not found, using embedded defaults.")
			return api.Config{}
		}
		log.WithError(err).
			WithField("filepath", confPath).
			Fatal("Failed to read config file.")
	}
	defer func() {
		if err := f.Close(); err != nil {
			log.WithError(err).Fatal("Closing config file resulted in error.")
		}
	}()

	raw, err := io.ReadAll(f)
	if err != nil {
		log.WithError(err).Fatal("Failed to read in config.")
	}
	conf := api.Config{}

	if err := json.Unmarshal(raw, &conf); err != nil {
		log.WithError(err).Fatal("failed to convert config into json.")
	}

	return conf
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
