package commands

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/rakyll/statik/fs"
	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/buildinfo"
	"github.com/skycoin/dmsg/disc"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/spf13/cobra"

	_ "github.com/skycoin/skywire/cmd/hypervisor/statik" // embedded static files
	"github.com/skycoin/skywire/pkg/hypervisor"
	"github.com/skycoin/skywire/pkg/restart"
	"github.com/skycoin/skywire/pkg/util/pathutil"
)

const configEnv = "SW_HYPERVISOR_CONFIG"

// nolint:gochecknoglobals
var (
	log = logging.MustGetLogger("hypervisor")

	configPath     string
	mock           bool
	mockEnableAuth bool
	mockVisors     int
	mockMaxTps     int
	mockMaxRoutes  int
	delay          string
)

// nolint:gochecknoinits
func init() {
	rootCmd.Flags().StringVarP(&configPath, "config", "c", "./hypervisor-config.json", "hypervisor config path")
	rootCmd.Flags().BoolVarP(&mock, "mock", "m", false, "whether to run hypervisor with mock data")
	rootCmd.Flags().BoolVar(&mockEnableAuth, "mock-enable-auth", false, "whether to enable user management in mock mode")
	rootCmd.Flags().IntVar(&mockVisors, "mock-visors", 5, "number of visors to have in mock mode")
	rootCmd.Flags().IntVar(&mockMaxTps, "mock-max-tps", 10, "max number of transports per mock visor")
	rootCmd.Flags().IntVar(&mockMaxRoutes, "mock-max-routes", 30, "max number of routes per visor")
	rootCmd.Flags().StringVar(&delay, "delay", "0ns", "start delay (deprecated)") // deprecated
}

// nolint:gochecknoglobals
var rootCmd = &cobra.Command{
	Use:   "hypervisor",
	Short: "Manages Skywire Visors",
	Run: func(_ *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}

		delayDuration, err := time.ParseDuration(delay)
		if err != nil {
			log.WithError(err).Error("Failed to parse delay duration.")
			delayDuration = time.Duration(0)
		}

		time.Sleep(delayDuration)

		restartCtx := restart.CaptureContext()
		restartCtx.RegisterLogger(log)

		conf := prepareConfig(args)

		assets, err := fs.New()
		if err != nil {
			log.Fatalf("Failed to obtain embedded static files: %v", err)
		}

		dmsgC := prepareDmsg(conf)

		// Prepare hypervisor.
		hv, err := hypervisor.New(conf, assets, restartCtx, dmsgC)
		if err != nil {
			log.Fatalln("Failed to start hypervisor:", err)
		}

		if mock {
			serveMockData(hv)
		} else {
			serveDmsg(ctx, hv, conf)
		}

		// Serve HTTP(s).
		log.WithField("addr", conf.HTTPAddr).
			WithField("tls", conf.EnableTLS).
			Info("Serving hypervisor...")

		if handler := hv.HTTPHandler(); conf.EnableTLS {
			err = http.ListenAndServeTLS(conf.HTTPAddr, conf.TLSCertFile, conf.TLSKeyFile, handler)
		} else {
			err = http.ListenAndServe(conf.HTTPAddr, handler)
		}

		if err != nil {
			log.WithError(err).Fatal("Hypervisor exited with error.")
		}

		log.Info("Good bye!")
	},
}

func prepareConfig(args []string) (conf hypervisor.Config) {
	if configPath == "" {
		configPath = pathutil.FindConfigPath(args, -1, configEnv, pathutil.HypervisorDefaults())
	}
	conf.FillDefaults(mock)
	if err := conf.Parse(configPath); err != nil {
		log.WithError(err).Fatalln("failed to parse config file")
	}
	log.WithField("config", conf).Info()
	return conf
}

func prepareDmsg(conf hypervisor.Config) *dmsg.Client {
	dmsgC := dmsg.NewClient(conf.PK, conf.SK, disc.NewHTTP(conf.DmsgDiscovery), dmsg.DefaultConfig())
	go dmsgC.Serve(context.Background())

	<-dmsgC.Ready()
	return dmsgC
}

func serveDmsg(ctx context.Context, hv *hypervisor.Hypervisor, conf hypervisor.Config) {
	go func() {
		if err := hv.ServeRPC(ctx, conf.DmsgPort); err != nil {
			log.WithError(err).Fatal("Failed to serve RPC client over dmsg.")
		}
	}()
	log.WithField("addr", dmsg.Addr{PK: conf.PK, Port: conf.DmsgPort}).
		Info("Serving RPC client over dmsg.")
}

func serveMockData(hv *hypervisor.Hypervisor) {
	err := hv.AddMockData(hypervisor.MockConfig{
		Visors:            mockVisors,
		MaxTpsPerVisor:    mockMaxTps,
		MaxRoutesPerVisor: mockMaxRoutes,
		EnableAuth:        mockEnableAuth,
	})
	if err != nil {
		log.Fatalln("Failed to add mock data:", err)
	}
}

// Execute executes root CLI command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
