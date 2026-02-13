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
	"net/http/pprof"
	"net/rpc"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/metricsutil"
)

var (
	metricsAddr  string
	tag          string
	cfgFromStdin bool
	pprofAddr    string
)

func init() {
	RootCmd.AddCommand(checkHealthCmd)
	RootCmd.Flags().StringVarP(&metricsAddr, "metrics", "m", "", "address to bind metrics API to")
	RootCmd.Flags().StringVar(&pprofAddr, "pprof", "", "address to bind pprof debug server (e.g. localhost:6060)")
	RootCmd.Flags().StringVar(&tag, "tag", "setup_node", "logging tag")
	RootCmd.Flags().BoolVarP(&cfgFromStdin, "stdin", "i", false, "read config from STDIN")
}

// RootCmd is the root command for setup node
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", ""))+" [config.json]", " ")[0]
	}(),
	Short: "Route Setup Node for skywire",
	Long:  calvin.AsciiFont("route-setup-node"),
	Run: func(_ *cobra.Command, args []string) {
		mLog := logging.NewMasterLogger()
		log := logging.MustGetLogger(tag)

		if _, err := buildinfo.Get().WriteTo(mLog.Out); err != nil {
			mLog.Printf("Failed to output build info: %v", err)
		}

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
				log.Infof("Starting pprof server on %s", pprofAddr)
				server := &http.Server{
					Addr:              pprofAddr,
					Handler:           pprofMux,
					ReadHeaderTimeout: 10 * time.Second,
					ReadTimeout:       30 * time.Second,
					WriteTimeout:      30 * time.Second,
					IdleTimeout:       60 * time.Second,
				}
				if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					log.Errorf("pprof server failed: %v", err)
				}
			}()

			time.Sleep(100 * time.Millisecond)
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

		m := prepareMetrics(log)

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()

		log.Fatal(sn.Serve(ctx, m))
	},
}

func prepareMetrics(log logrus.FieldLogger) setupmetrics.Metrics {
	if metricsAddr == "" {
		return setupmetrics.NewEmpty()
	}

	m := setupmetrics.NewVictoriaMetrics()

	metricsutil.ServeHTTPMetrics(log, metricsAddr)

	// TODO (darkrengarius): implement these with Victoria Metrics somehow
	//reg.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	//reg.MustRegister(prometheus.NewGoCollector())

	return m
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
