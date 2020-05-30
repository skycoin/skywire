package commands

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	_ "net/http/pprof" //nolint:gosec // https://golang.org/doc/diagnostics.html#profiling
	"os"

	"github.com/SkycoinProject/dmsg/buildinfo"
	"github.com/SkycoinProject/dmsg/cmdutil"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/pkg/profile"
	"github.com/spf13/cobra"

	"github.com/SkycoinProject/skywire-mainnet/pkg/restart"
	"github.com/SkycoinProject/skywire-mainnet/pkg/syslog"
	"github.com/SkycoinProject/skywire-mainnet/pkg/util/pathutil"
	"github.com/SkycoinProject/skywire-mainnet/pkg/visor"
	"github.com/SkycoinProject/skywire-mainnet/pkg/visor/visorconfig"
)

var restartCtx = restart.CaptureContext()

const configEnv = "SW_VISOR_CONFIG"

var (
	tag        string
	syslogAddr string
	pprofMode  string
	pprofAddr  string
	confPath   string
)

func init() {
	rootCmd.Flags().StringVar(&tag, "tag", "skywire", "logging tag")
	rootCmd.Flags().StringVar(&syslogAddr, "syslog", "", "syslog server address. E.g. localhost:514")
	rootCmd.Flags().StringVarP(&pprofMode, "pprofmode", "p", "", "pprof profiling mode. Valid values: cpu, mem, mutex, block, trace, http")
	rootCmd.Flags().StringVar(&pprofAddr, "pprofaddr", "localhost:6060", "pprof http port if mode is 'http'")
	rootCmd.Flags().StringVarP(&confPath, "config", "c", "skywire-config.json", "config file location. If the value is 'STDIN', config file will be read from stdin.")
}

var rootCmd = &cobra.Command{
	Use:   "skywire-visor",
	Short: "Skywire visor",
	Run: func(_ *cobra.Command, args []string) {

		log := initLogger(tag, syslogAddr)

		if _, err := buildinfo.Get().WriteTo(log.Out); err != nil {
			log.WithError(err).Error("Failed to output build info.")
		}

		stopPProf := initPProf(log, tag, pprofMode, pprofAddr)
		defer stopPProf()

		conf := initConfig(log, args, confPath)

		v, ok := visor.NewVisor(conf, restartCtx)
		if !ok {
			log.Fatal("Failed to start visor.")
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()

		// Wait.
		<-ctx.Done()

		if err := v.Close(); err != nil {
			log.WithError(err).Error("Visor closed with error.")
		}
	},
	Version: buildinfo.Get().Version,
}

// Execute executes root CLI command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
	}
}

func initLogger(tag string, syslogAddr string) *logging.MasterLogger {
	log := logging.NewMasterLogger()

	if syslogAddr != "" {
		hook, err := syslog.SetupHook(syslogAddr, tag)
		if err != nil {
			log.WithError(err).Error("Failed to connect to the syslog daemon.")
		} else {
			log.AddHook(hook)
			log.Out = ioutil.Discard
		}
	}

	return log
}

func initPProf(log *logging.MasterLogger, tag string, profMode string, profAddr string) (stop func()) {
	var optFunc func(*profile.Profile)

	switch profMode {
	case "none", "":
	case "http":
		go func() {
			err := http.ListenAndServe(profAddr, nil)
			log.WithError(err).
				WithField("mode", profMode).
				WithField("addr", profAddr).
				Info("Stopped serving pprof on http.")
		}()
	case "cpu":
		optFunc = profile.CPUProfile
	case "mem":
		optFunc = profile.MemProfile
	case "mutex":
		optFunc = profile.MutexProfile
	case "block":
		optFunc = profile.BlockProfile
	case "trace":
		optFunc = profile.TraceProfile
	}

	if optFunc != nil {
		stop = profile.Start(profile.ProfilePath("./logs/"+tag), optFunc).Stop
	}

	if stop == nil {
		stop = func() {}
	}
	return stop
}

func initConfig(mLog *logging.MasterLogger, args []string, confPath string) *visorconfig.V1 {
	log := mLog.PackageLogger("visor:config")

	var r io.Reader

	switch confPath {
	case visorconfig.StdinName:
		log.Info("Reading config from STDIN.")
		r = os.Stdin
	case "":
		confPath = pathutil.FindConfigPath(args, -1, configEnv, pathutil.VisorDefaults())
		fallthrough
	default:
		log.WithField("filepath", confPath).Info("Reading config from file.")
		f, err := os.Open(confPath) //nolint:gosec
		if err != nil {
			log.WithError(err).
				WithField("filepath", confPath).
				Fatal("Failed to read config file.")
		}
		defer func() {
			if err := f.Close(); err != nil {
				log.WithError(err).Error("Closing config file resulted in error.")
			}
		}()
		r = f
	}

	raw, err := ioutil.ReadAll(r)
	if err != nil {
		log.WithError(err).Fatal("Failed to read in config.")
	}

	conf, err := visorconfig.Parse(mLog, confPath, raw)
	if err != nil {
		log.WithError(err).Fatal("Failed to parse config.")
	}

	return conf
}
