// Package commands root.go
package commands

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/rpc"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/tidwall/pretty"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/dmsgc"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	"github.com/skycoin/skywire/pkg/transport/tpdclient"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

var (
	metricsAddr  string
	tag          string
	cfgFromStdin bool
	pprofMode    string
	pprofAddr    string
)

// exampleJSON marshals v to indented JSON with color, returning empty string on error
func exampleJSON(v interface{}) string {
	b, err := json.MarshalIndent(v, "    ", "  ")
	if err != nil {
		return ""
	}
	return string(pretty.Color(b, nil))
}

// generateExamples creates example config
func generateExamples() string {
	return fmt.Sprintf(`
Example Config:
  %s

Generate Keys:
  skywire cli config gen-keys | tee sn-keys.txt
  # Line 1: public_key, Line 2: secret_key`,
		exampleJSON(router.SetupConfig{
			Dmsg: dmsgc.DmsgConfig{
				Discovery:     deployment.Prod.DmsgDiscovery,
				SessionsCount: 1,
			},
			TransportDiscovery: deployment.Prod.TransportDiscovery,
		}),
	)
}

func init() {
	RootCmd.AddCommand(checkHealthCmd)
	RootCmd.Flags().StringVarP(&metricsAddr, "metrics", "m", "", "address to bind metrics API to")
	RootCmd.Flags().StringVarP(&pprofMode, "pprofmode", "q", "", "[ http ] pprof mode")
	RootCmd.Flags().StringVarP(&pprofAddr, "pprofaddr", "r", "localhost:6060", "pprof http port")
	RootCmd.Flags().StringVar(&tag, "tag", "setup_node", "logging tag\n\r")
	RootCmd.Flags().BoolVarP(&cfgFromStdin, "stdin", "i", false, "read config from STDIN")
}

// RootCmd is the root command for setup node
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", ""))+" [config.json]", " ")[0]
	}(),
	Short: "Route Setup Node for skywire",
	Long: calvin.AsciiFont("route-setup-node") + `
Route Setup Node - establishes routes between visors via dmsg RPC.

Listens on dmsg port ` + fmt.Sprintf("%d", skyenv.DmsgSetupPort) + ` for route setup requests from visors.

RPC Methods (via dmsg):
  SetupRPCGateway.DialRouteGroup    Establish bidirectional route
  SetupRPCGateway.HealthCheck       Health check
` + generateExamples() + `

Usage:
  skywire svc sn [config.json]
  skywire cli config gen --sn -o sn-config.json
  skywire cli config gen --sn | skywire svc sn -i`,
	Run: func(_ *cobra.Command, args []string) {
		mLog := logging.NewMasterLogger()
		log := logging.MustGetLogger(tag)

		if _, err := buildinfo.Get().WriteTo(mLog.Out); err != nil {
			mLog.Printf("Failed to output build info: %v", err)
		}

		if pprofMode == "http" {
			metricsutil.ServePProf(log, pprofAddr, "setup-node")
		}

		var rdr io.Reader
		var err error

		if !cfgFromStdin {
			configFile := "config.json"

			if len(args) > 0 {
				configFile = args[0]
			}
			rdr, err = os.Open(configFile)
			if err != nil {
				log.Fatalf("Failed to open config: %v", err)
			}
		} else {
			log.Info("Reading config from STDIN")
			rdr = bufio.NewReader(os.Stdin)
		}

		conf := &router.SetupConfig{}

		raw, err := io.ReadAll(rdr)
		if err != nil {
			log.Fatalf("Failed to read config: %v", err)
		}

		if err := json.Unmarshal(raw, &conf); err != nil {
			log.WithField("raw", string(raw)).Fatalf("Failed to decode config: %s", err)
		}

		log.Infof("Config: %#v", conf)

		sn, err := router.NewNode(conf)
		if err != nil {
			log.Fatal("Failed to create a setup node: ", err)
		}

		collector := prepareMetrics(log)

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()

		// Initialize transport manager for cascade route setup.
		// The RSN uses STCPR transports with label "setup" to reach
		// visors directly, enabling route setup without DMSG.
		if conf.Transport != nil && conf.Cascade != nil {
			tpLabel := transport.LabelSetup
			if conf.Transport.DefaultLabel != "" {
				tpLabel = transport.Label(conf.Transport.DefaultLabel)
			}

			var tpdC transport.DiscoveryClient
			if conf.TransportDiscovery != "" {
				var tpdErr error
				tpdC, tpdErr = tpdclient.NewHTTP(
					conf.TransportDiscovery,
					conf.PK, conf.SK,
					&http.Client{}, "",
					mLog,
				)
				if tpdErr != nil {
					log.WithError(tpdErr).Warn("Failed to create TPD client — using mock")
					tpdC = transport.NewDiscoveryMock()
				}
			} else {
				tpdC = transport.NewDiscoveryMock()
			}

			logS := transport.InMemoryTransportLogStore()
			tpMConf := transport.ManagerConfig{
				PubKey:           conf.PK,
				SecKey:           conf.SK,
				DiscoveryClient:  tpdC,
				LogStore:         logS,
				Version:          buildinfo.Version(),
				ARTransportLimit: -1, // RSN never registers with AR
			}

			var arC addrresolver.APIClient
			if conf.Transport.AddressResolver != "" {
				arC, _ = addrresolver.NewHTTP( //nolint:errcheck
					conf.Transport.AddressResolver,
					conf.PK, conf.SK,
					&http.Client{}, "",
					log, mLog,
				)
			}

			factory := network.ClientFactory{
				PK:         conf.PK,
				SK:         conf.SK,
				ListenAddr: conf.Transport.STCPRAddr,
				ARClient:   arC,
				EB:         appevent.NewBroadcaster(log, 0),
				MLogger:    mLog,
			}

			tpM, tpErr := transport.NewManager(log, arC, factory.EB, &tpMConf, factory)
			if tpErr != nil {
				log.WithError(tpErr).Warn("Failed to create transport manager — cascade disabled")
			} else {
				tpM.InitClient(ctx, types.STCPR, 0)
				log.WithField("stcpr_addr", conf.Transport.STCPRAddr).
					WithField("label", tpLabel).
					Info("RSN transport manager started (STCPR)")

				sn.InitCascade(conf, tpM)
				_ = tpLabel // label is set per-transport at dial time
			}
		}

		// Start DMSG HTTP health server using the setup-node's own DMSG client.
		// This avoids creating a second DMSG client with the same PK, which
		// causes "error 306 - no associated listener" when the DMSG server
		// routes streams to the wrong client.
		{
			startedAt := time.Now()
			dmsgAddr := fmt.Sprintf("%s:%d", conf.PK.Hex(), dmsg.DefaultDmsgHTTPPort)

			healthMux := http.NewServeMux()
			healthMux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				resp := httputil.HealthCheckResponse{
					ServiceName: "setup-node",
					BuildInfo:   buildinfo.Get(),
					StartedAt:   startedAt,
					DmsgAddr:    dmsgAddr,
					DmsgServers: sn.DmsgClient().ConnectedServersPK(),
				}
				json.NewEncoder(w).Encode(resp) //nolint:errcheck,gosec
			})

			// Public stats endpoint — aggregate counters, latency percentiles,
			// failure breakdown. Recent failures are sanitized (no src/dst PKs).
			healthMux.HandleFunc("/stats", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				snap := collector.Snapshot()
				// Sanitize: strip src/dst PKs from recent failures and top destinations
				// to avoid leaking which visors are communicating with each other.
				for i := range snap.RecentFailures {
					snap.RecentFailures[i].SrcPK = ""
					snap.RecentFailures[i].DstPK = ""
				}
				for i := range snap.TopDestinations {
					snap.TopDestinations[i].PK = ""
				}
				for i := range snap.TopFailedDestinations {
					snap.TopFailedDestinations[i].PK = ""
				}
				json.NewEncoder(w).Encode(snap) //nolint:errcheck,gosec
			})

			// Use the setup-node's own DMSG client for health/debug endpoints
			snClient := sn.DmsgClient()
			go func() {
				if err := dmsghttp.ListenAndServe(ctx, conf.SK, healthMux, nil,
					dmsg.DefaultDmsgHTTPPort, snClient, log); err != nil {
					log.WithError(err).Error("DMSG HTTP health server stopped")
				}
			}()
			go func() {
				if err := dmsghttp.ServeDebug(ctx, snClient, log, deployment.Prod.SurveyWhitelist); err != nil {
					log.WithError(err).Error("DMSG HTTP debug server stopped")
				}
			}()
			log.Infof("DMSG HTTP health endpoint available at %s", dmsgAddr)
		}

		log.Fatal(sn.Serve(ctx, collector))
	},
}

func prepareMetrics(log logrus.FieldLogger) *setupmetrics.Collector {
	collector := setupmetrics.NewCollector(setupmetrics.CollectorConfig{})

	if metricsAddr != "" {
		// Victoria Metrics publishes Prometheus gauges alongside the Collector.
		_ = setupmetrics.NewVictoriaMetrics()
		metricsutil.ServeHTTPMetrics(log, metricsAddr)
	}

	return collector
}

// checkHealthCmd does health check on running route setup node
var checkHealthCmd = &cobra.Command{
	Use:   "health <pk>",
	Short: "Health check of route setup node",
	Long:  "Health check of route setup node",
	Run: func(_ *cobra.Command, args []string) {
		if len(args) != 1 {
			fmt.Println("supply setup node public key as argument")
			os.Exit(1)
		}

		var snpk cipher.PubKey
		if err := snpk.Set(args[0]); err != nil {
			log.Fatalf("Invalid setup node public key: %v", err)
		}

		// generate keys to useae for dmsg client
		pk, sk := cipher.GenerateKeyPair()

		// Create logger
		log := logging.MustGetLogger("health-check")

		// Start DMSG client
		ctx := context.Background()
		dmsgDisc := disc.NewHTTP(deployment.Prod.DmsgDiscovery, &http.Client{}, log)
		dmsgC := dmsg.NewClient(pk, sk, dmsgDisc, &dmsg.Config{MinSessions: 1})

		go dmsgC.Serve(ctx)
		log.Info("Connecting to DMSG network...")
		<-dmsgC.Ready()
		log.Info("Connected to DMSG network")
		log.Infoln("dialing route setup-node: ", snpk.String())

		// Dial setup node
		addr := dmsg.Addr{PK: snpk, Port: skyenv.DmsgSetupPort}
		conn, err := dmsgC.Dial(ctx, addr)
		if err != nil {
			log.Fatalf("Failed to dial setup node: %v", err)
		}
		//nolint:errcheck
		defer conn.Close()

		log.Infoln("Connected to setup node: ", snpk.String())

		// RPC client
		client := rpc.NewClient(conn)
		//nolint:errcheck
		defer client.Close()

		// Call HealthCheck
		var argsRPC router.HealthCheckArgs
		var reply router.HealthCheckReply
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		call := client.Go("SetupRPCGateway.HealthCheck", &argsRPC, &reply, nil)

		select {
		case <-callCtx.Done():
			log.Fatal("HealthCheck timed out")
		case <-call.Done:
			if call.Error != nil {
				log.Fatalf("RPC error: %v", call.Error)
			}
		}

		fmt.Println("Health check OK:", reply.Status)
	},
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
