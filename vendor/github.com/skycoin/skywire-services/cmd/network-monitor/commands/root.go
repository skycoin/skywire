// Package commands cmd/network-monitor/commands/root.go
package commands

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/metricsutil"
	"github.com/skycoin/skywire-utilities/pkg/storeconfig"
	"github.com/skycoin/skywire-utilities/pkg/tcpproxy"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-services/internal/nmmetrics"
	"github.com/skycoin/skywire-services/pkg/network-monitor/api"
	"github.com/skycoin/skywire-services/pkg/network-monitor/store"
)

const (
	redisScheme = "redis://"
)

var (
	sleepDeregistration time.Duration
	confPath            string
	sdURL               string
	arURL               string
	utURL               string
	addr                string
	tag                 string
	logLvl              string
	metricsAddr         string
	redisURL            string
	testing             bool
	redisPoolSize       int
	batchSize           int
)

func init() {
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9080", "address to bind to.\033[0m")
	RootCmd.Flags().DurationVar(&sleepDeregistration, "sleep-deregistration", 10, "Sleep time for derigstration process in minutes\033[0m")
	RootCmd.Flags().IntVarP(&batchSize, "batchsize", "b", 30, "Batch size of deregistration\033[0m")
	RootCmd.Flags().StringVarP(&confPath, "config", "c", "network-monitor.json", "config file location.\033[0m")
	RootCmd.Flags().StringVarP(&sdURL, "sd-url", "n", "", "url to service discovery.\033[0m")
	RootCmd.Flags().StringVarP(&arURL, "ar-url", "v", "", "url to address resolver.\033[0m")
	RootCmd.Flags().StringVarP(&utURL, "ut-url", "u", "", "url to uptime tracker visor data.\033[0m")
	RootCmd.Flags().StringVar(&tag, "tag", "network_monitor", "logging tag\033[0m")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "info", "set log level one of: info, error, warn, debug, trace, panic")
	RootCmd.Flags().StringVarP(&metricsAddr, "metrics", "m", "", "address to bind metrics API to\033[0m")
	RootCmd.Flags().StringVar(&redisURL, "redis", "redis://localhost:6379", "connections string for a redis store\033[0m")
	RootCmd.Flags().BoolVarP(&testing, "testing", "t", false, "enable testing to start without redis\033[0m")
	RootCmd.Flags().IntVar(&redisPoolSize, "redis-pool-size", 10, "redis connection pool size\033[0m")
}

// RootCmd contains the root command
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "Network monitor for skywire VPN and Visor.",
	Long: `
	┌┐┌┌─┐┌┬┐┬ ┬┌─┐┬─┐┬┌─   ┌┬┐┌─┐┌┐┌┬┌┬┐┌─┐┬─┐
	│││├┤  │ ││││ │├┬┘├┴┐───││││ │││││ │ │ │├┬┘
	┘└┘└─┘ ┴ └┴┘└─┘┴└─┴ ┴   ┴ ┴└─┘┘└┘┴ ┴ └─┘┴└─`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {
		if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}

		if !strings.HasPrefix(redisURL, redisScheme) {
			redisURL = redisScheme + redisURL
		}

		storeConfig := storeconfig.Config{
			Type:     storeconfig.Redis,
			URL:      redisURL,
			Password: storeconfig.RedisPassword(),
			PoolSize: redisPoolSize,
		}

		if testing {
			storeConfig.Type = storeconfig.Memory
		}

		s, err := store.New(storeConfig)
		if err != nil {
			log.Fatal("Failed to initialize redis store: ", err)
		}

		mLogger := logging.NewMasterLogger()
		lvl, err := logging.LevelFromString(logLvl)
		if err != nil {
			mLogger.Fatal("Invalid loglvl detected")
		}

		logging.SetLevel(lvl)
		conf := api.InitConfig(confPath, mLogger)

		if sdURL == "" {
			sdURL = conf.Launcher.ServiceDisc
		}
		if arURL == "" {
			arURL = conf.Transport.AddressResolver
		}
		if utURL == "" {
			utURL = conf.UptimeTracker.Addr + "/uptimes"
		}

		var srvURLs api.ServicesURLs
		srvURLs.SD = sdURL
		srvURLs.AR = arURL
		srvURLs.UT = utURL

		logger := mLogger.PackageLogger("network_monitor")

		logger.WithField("addr", addr).Info("Serving discovery API...")

		metricsutil.ServeHTTPMetrics(logger, metricsAddr)

		var m nmmetrics.Metrics
		if metricsAddr == "" {
			m = nmmetrics.NewEmpty()
		} else {
			m = nmmetrics.NewVictoriaMetrics()
		}
		enableMetrics := metricsAddr != ""

		nmSign, _ := cipher.SignPayload([]byte(conf.PK.Hex()), conf.SK) //nolint

		var nmConfig api.NetworkMonitorConfig
		nmConfig.PK = conf.PK
		nmConfig.SK = conf.SK
		nmConfig.Sign = nmSign
		nmConfig.BatchSize = batchSize

		nmAPI := api.New(s, logger, srvURLs, enableMetrics, m, nmConfig)

		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()

		go nmAPI.InitDeregistrationLoop(ctx, conf, sleepDeregistration)

		go func() {
			if err := tcpproxy.ListenAndServe(addr, nmAPI); err != nil {
				logger.Errorf("serve: %v", err)
				cancel()
			}
		}()

		<-ctx.Done()
		if err := nmAPI.Visor.Close(); err != nil {
			logger.WithError(err).Error("Visor closed with error.")
		}
	},
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
