// Package commands cmd/config-bootstrapper/commands/root.go
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/skycoin/dmsg/pkg/direct"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsghttp"
	"github.com/spf13/cobra"
	"github.com/tidwall/pretty"

	"github.com/skycoin/skywire/pkg/config-bootstrapper/api"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/tcpproxy"
)

var (
	addr           string
	tag            string
	stunPath       string
	domain         string
	dmsgDisc       string
	sk             cipher.SecKey
	dmsgPort       uint16
	dmsgServerType string
	pprofAddr      string
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
	RootCmd.Flags().Uint16Var(&dmsgPort, "dmsgPort", dmsg.DefaultDmsgHTTPPort, "dmsg port value\n\r")
	RootCmd.Flags().StringVar(&dmsgServerType, "dmsg-server-type", "", "type of dmsg server on dmsghttp handler")
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

		if pprofAddr != "" {
			pprofMux := http.NewServeMux()

			// Register the index (which links to everything else)
			pprofMux.HandleFunc("/debug/pprof/", pprof.Index)
			pprofMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
			pprofMux.HandleFunc("/debug/pprof/profile", pprof.Profile)
			pprofMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
			pprofMux.HandleFunc("/debug/pprof/trace", pprof.Trace)

			// Register profile handlers using pprof.Handler
			for _, profile := range []string{"heap", "goroutine", "threadcreate", "block", "mutex", "allocs"} {
				pprofMux.Handle("/debug/pprof/"+profile, pprof.Handler(profile))
			}

			go func() {
				logger.Infof("Starting pprof server on %s", pprofAddr)
				server := &http.Server{
					Addr:              pprofAddr,
					Handler:           pprofMux,
					ReadHeaderTimeout: 10 * time.Second,
					ReadTimeout:       30 * time.Second,
					WriteTimeout:      30 * time.Second,
					IdleTimeout:       60 * time.Second,
				}
				if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					logger.Errorf("pprof server failed: %v", err)
				}
			}()

			time.Sleep(100 * time.Millisecond)
		}

		config := readConfig(logger, stunPath)

		pk, err := sk.PubKey()
		if err != nil {
			logger.WithError(err).Warn("No SecKey found. Skipping serving on dmsghttp.")
		}

		var dmsgAddr string
		if !pk.Null() {
			dmsgAddr = fmt.Sprintf("%s:%d", pk.Hex(), dmsgPort)
		}

		conAPI := api.New(logger, config, domain, dmsgAddr)
		if logger != nil {
			logger.Infof("Listening on %s", addr)
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()

		if !pk.Null() {
			servers := dmsghttp.GetServers(ctx, dmsgDisc, dmsgServerType, logger)

			var keys cipher.PubKeys
			keys = append(keys, pk)
			dClient := direct.NewClient(direct.GetAllEntries(keys, servers), logger)
			config := &dmsg.Config{
				MinSessions:          0, // listen on all available servers
				UpdateInterval:       dmsg.DefaultUpdateInterval,
				ConnectedServersType: dmsgServerType,
			}

			dmsgDC, closeDmsgDC, err := direct.StartDmsg(ctx, logger, pk, sk, dClient, config)
			if err != nil {
				logger.WithError(err).Fatal("failed to start direct dmsg client.")
			}

			defer closeDmsgDC()

			go dmsghttp.UpdateServers(ctx, dClient, dmsgDisc, dmsgDC, dmsgServerType, logger)

			go func() {
				if err := dmsghttp.ListenAndServe(ctx, sk, conAPI, dClient, dmsgPort, dmsgDC, logger); err != nil {
					logger.Errorf("dmsghttp.ListenAndServe: %v", err)
					cancel()
				}
			}()
		}

		go func() {
			if err := tcpproxy.ListenAndServe(addr, conAPI); err != nil {
				logger.Errorf("conAPI.ListenAndServe: %v", err)
				cancel()
			}
		}()

		<-ctx.Done()

		conAPI.Close()
	},
}

func readConfig(log *logging.Logger, confPath string) (config api.Config) {
	var r io.Reader

	f, err := os.Open(confPath) //nolint:gosec
	if err != nil {
		log.WithError(err).
			WithField("filepath", confPath).
			Fatal("Failed to read config file.")
	}
	defer func() {
		if err := f.Close(); err != nil {
			log.WithError(err).Fatal("Closing config file resulted in error.")
		}
	}()

	r = f

	raw, err := io.ReadAll(r)
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
