package commands

// NOTE: "net/http/pprof" is used for profiling.
import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	_ "net/http/pprof" // nolint:gosec // TODO: consider removing for security reasons
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/pkg/profile"
	"github.com/spf13/cobra"

	"github.com/SkycoinProject/skywire-mainnet/pkg/restart"
	"github.com/SkycoinProject/skywire-mainnet/pkg/syslog"
	"github.com/SkycoinProject/skywire-mainnet/pkg/util/buildinfo"
	"github.com/SkycoinProject/skywire-mainnet/pkg/util/pathutil"
	"github.com/SkycoinProject/skywire-mainnet/pkg/visor"
)

// TODO(evanlinjin): Determine if this is still needed.
//import _ "net/http/pprof" // used for HTTP profiling

const configEnv = "SW_CONFIG"

type runConf struct {
	syslogAddr    string
	tag           string
	confFromStdin bool
	profileMode   string
	port          string
	startDelay    string
	args          []string

	profileStop  func()
	logger       *logging.Logger
	masterLogger *logging.MasterLogger
	conf         *visor.Config
	visor        *visor.Visor
	restartCtx   *restart.Context
}

var conf *runConf

var rootCmd = &cobra.Command{
	Use:   "skywire-visor [config-path]",
	Short: "Visor for skywire",
	Run: func(_ *cobra.Command, args []string) {
		if _, err := buildinfo.Get().WriteTo(log.Writer()); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}

		conf.args = args

		conf.startProfiler().
			startLogger().
			readConfig().
			runVisor().
			waitOsSignals().
			stopVisor()
	},
	Version: buildinfo.Get().Version,
}

func init() {
	conf = new(runConf)
	rootCmd.Flags().StringVarP(&conf.syslogAddr, "syslog", "", "none", "syslog server address. E.g. localhost:514")
	rootCmd.Flags().StringVarP(&conf.tag, "tag", "", "skywire", "logging tag")
	rootCmd.Flags().BoolVarP(&conf.confFromStdin, "stdin", "i", false, "read config from STDIN")
	rootCmd.Flags().StringVarP(&conf.profileMode, "profile", "p", "none", "enable profiling with pprof. Mode:  none or one of: [cpu, mem, mutex, block, trace, http]")
	rootCmd.Flags().StringVarP(&conf.port, "port", "", "6060", "port for http-mode of pprof")
	rootCmd.Flags().StringVarP(&conf.startDelay, "delay", "", "0ns", "delay before visor start")

	conf.restartCtx = restart.CaptureContext()
}

// Execute executes root CLI command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}

func (rc *runConf) startProfiler() *runConf {
	var option func(*profile.Profile)

	switch rc.profileMode {
	case "none":
		rc.profileStop = func() {}
		return rc
	case "http":
		go func() {
			log.Println(http.ListenAndServe(fmt.Sprintf("localhost:%v", rc.port), nil))
		}()

		rc.profileStop = func() {}

		return rc
	case "cpu":
		option = profile.CPUProfile
	case "mem":
		option = profile.MemProfile
	case "mutex":
		option = profile.MutexProfile
	case "block":
		option = profile.BlockProfile
	case "trace":
		option = profile.TraceProfile
	}

	rc.profileStop = profile.Start(profile.ProfilePath("./logs/"+rc.tag), option).Stop

	return rc
}

func (rc *runConf) startLogger() *runConf {
	rc.masterLogger = logging.NewMasterLogger()
	rc.logger = rc.masterLogger.PackageLogger(rc.tag)

	if rc.syslogAddr != "none" {
		hook, err := syslog.SetupHook(rc.syslogAddr, rc.tag)
		if err != nil {
			rc.logger.Errorf("Error setting up syslog: %v", err)
		} else {
			rc.masterLogger.AddHook(hook)
			rc.masterLogger.Out = ioutil.Discard
		}
	}

	return rc
}

func (rc *runConf) readConfig() *runConf {
	var reader io.Reader
	var confPath string

	if !rc.confFromStdin {
		cp := pathutil.FindConfigPath(rc.args, 0, configEnv, pathutil.VisorDefaults())

		file, err := os.Open(filepath.Clean(cp))
		if err != nil {
			rc.logger.WithError(err).Fatal("Failed to open config file.")
		}
		defer func() {
			if err := file.Close(); err != nil {
				rc.logger.WithError(err).Warn("Failed to close config file.")
			}
		}()

		rc.logger.WithField("file", cp).Info("Reading config from file...")
		reader = file
		confPath = cp

	} else {
		rc.logger.Info("Reading config from STDIN...")
		reader = bufio.NewReader(os.Stdin)
		confPath = visor.StdinName
	}

	rc.conf = visor.BaseConfig(rc.masterLogger, confPath)
	dec := json.NewDecoder(reader)
	dec.DisallowUnknownFields()

	if err := dec.Decode(rc.conf); err != nil {
		rc.logger.WithError(err).Fatal("Failed to decode config.")
	}
	if err := rc.conf.Flush(); err != nil {
		rc.logger.WithError(err).Fatal("Failed to flush config.")
	}
	return rc
}

func (rc *runConf) runVisor() *runConf {
	startDelay, err := time.ParseDuration(rc.startDelay)
	if err != nil {
		rc.logger.Warnf("Using no visor start delay due to parsing failure: %v", err)

		startDelay = time.Duration(0)
	}

	if startDelay != 0 {
		rc.logger.Infof("Visor start delay is %v, waiting...", startDelay)
	}

	time.Sleep(startDelay)

	if rc.conf.Dmsgpty != nil && runtime.GOOS != "windows" {
		if err := visor.UnlinkSocketFiles(rc.conf.Dmsgpty.CLIAddr); err != nil {
			rc.logger.Fatal("failed to unlink socket files: ", err)
		}
	}

	v, ok := visor.NewVisor(rc.conf, rc.restartCtx)
	if !ok {
		rc.logger.Fatal("Failed to start visor.")
	}

	rc.visor = v
	return rc
}

func (rc *runConf) stopVisor() *runConf {
	defer rc.profileStop()

	if err := rc.visor.Close(); err != nil {
		rc.logger.WithError(err).Fatal("Failed to close visor.")
	}
	return rc
}

func (rc *runConf) waitOsSignals() *runConf {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT}...)
	<-ch

	go func() {
		select {
		case <-time.After(time.Duration(rc.conf.ShutdownTimeout)):
			rc.logger.Fatal("Timeout reached: terminating")
		case s := <-ch:
			rc.logger.Fatalf("Received signal %s: terminating", s)
		}
	}()

	return rc
}
