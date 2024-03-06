// Package commands cmd/liveness-checker/commands/root.go
package commands

import (
	"context"
	"fmt"
	"log"
	"log/syslog"
	"os"
	"path/filepath"
	"strings"

	logrussyslog "github.com/sirupsen/logrus/hooks/syslog"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/storeconfig"
	"github.com/skycoin/skywire-utilities/pkg/tcpproxy"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-services/pkg/liveness-checker/api"
	"github.com/skycoin/skywire-services/pkg/liveness-checker/store"
)

const (
	redisScheme = "redis://"
)

var (
	confPath   string
	addr       string
	tag        string
	syslogAddr string
	logLvl     string
	redisURL   string
	testing    bool
)

func init() {
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9081", "address to bind to.\033[0m")
	RootCmd.Flags().StringVarP(&confPath, "config", "c", "liveness-checker.json", "config file location.\033[0m")
	RootCmd.Flags().StringVar(&tag, "tag", "liveness_checker", "logging tag\033[0m")
	RootCmd.Flags().StringVar(&syslogAddr, "syslog", "", "syslog server address. E.g. localhost:514\033[0m")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "info", "set log level one of: info, error, warn, debug, trace, panic")
	RootCmd.Flags().StringVar(&redisURL, "redis", "redis://localhost:6379", "connections string for a redis store\033[0m")
	RootCmd.Flags().BoolVarP(&testing, "testing", "t", false, "enable testing to start without redis\033[0m")
}

// RootCmd contains the root command
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "Liveness checker of the deployment.",
	Long: `
	┬  ┬┬  ┬┌─┐┌┐┌┌─┐┌─┐┌─┐   ┌─┐┬ ┬┌─┐┌─┐┬┌─┌─┐┬─┐
	│  │└┐┌┘├┤ │││├┤ └─┐└─┐───│  ├─┤├┤ │  ├┴┐├┤ ├┬┘
	┴─┘┴ └┘ └─┘┘└┘└─┘└─┘└─┘   └─┘┴ ┴└─┘└─┘┴ ┴└─┘┴└─`,
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
		}

		if testing {
			storeConfig.Type = storeconfig.Memory
		}

		mLogger := logging.NewMasterLogger()
		lvl, err := logging.LevelFromString(logLvl)
		if err != nil {
			mLogger.Fatal("Invalid loglvl detected")
		}

		logging.SetLevel(lvl)

		conf, confAPI := api.InitConfig(confPath, mLogger)

		logger := mLogger.PackageLogger(tag)
		if syslogAddr != "" {
			hook, err := logrussyslog.NewSyslogHook("udp", syslogAddr, syslog.LOG_INFO, tag)
			if err != nil {
				logger.Fatalf("Unable to connect to syslog daemon on %v", syslogAddr)
			}
			logging.AddHook(hook)
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()

		s, err := store.New(ctx, storeConfig, logger)
		if err != nil {
			logger.Fatal("Failed to initialize redis store: ", err)
		}

		logger.WithField("addr", addr).Info("Serving discovery API...")

		lcAPI := api.New(conf.PK, conf.SK, s, logger, mLogger, confAPI)

		go lcAPI.RunBackgroundTasks(ctx, conf)

		go func() {
			if err := tcpproxy.ListenAndServe(addr, lcAPI); err != nil {
				logger.Errorf("serve: %v", err)
				cancel()
			}
		}()

		<-ctx.Done()
		if err := lcAPI.Visor.Close(); err != nil {
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
