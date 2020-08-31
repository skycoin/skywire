package commands

// NOTE: "net/http/pprof" is used for profiling.
import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"log/syslog"
	"net/http"
	_ "net/http/pprof" // nolint:gosec // TODO: consider removing for security reasons
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/profile"
	logrussyslog "github.com/sirupsen/logrus/hooks/syslog"
	"github.com/skycoin/dmsg/discord"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/internal/utclient"
	"github.com/skycoin/skywire/pkg/restart"
	"github.com/skycoin/skywire/pkg/util/buildinfo"
	"github.com/skycoin/skywire/pkg/util/pathutil"
	"github.com/skycoin/skywire/pkg/visor"
)

// TODO(evanlinjin): Determine if this is still needed.
//import _ "net/http/pprof" // used for HTTP profiling

const configEnv = "SW_CONFIG"
const defaultShutdownTimeout = visor.Duration(10 * time.Second)

type runCfg struct {
	syslogAddr   string
	tag          string
	cfgFromStdin bool
	profileMode  string
	port         string
	startDelay   string
	args         []string

	profileStop  func()
	logger       *logging.Logger
	masterLogger *logging.MasterLogger
	conf         visor.Config
	visor        *visor.Visor
	restartCtx   *restart.Context
}

var cfg *runCfg

var rootCmd = &cobra.Command{
	Use:   "skywire-visor [config-path]",
	Short: "Visor for skywire",
	Run: func(_ *cobra.Command, args []string) {
		if _, err := buildinfo.Get().WriteTo(log.Writer()); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}

		cfg.args = args

		cfg.startProfiler().
			startLogger().
			readConfig().
			runVisor().
			waitOsSignals().
			stopVisor()
	},
	Version: buildinfo.Get().Version,
}

func init() {
	cfg = &runCfg{}
	rootCmd.Flags().StringVarP(&cfg.syslogAddr, "syslog", "", "none", "syslog server address. E.g. localhost:514")
	rootCmd.Flags().StringVarP(&cfg.tag, "tag", "", "skywire", "logging tag")
	rootCmd.Flags().BoolVarP(&cfg.cfgFromStdin, "stdin", "i", false, "read config from STDIN")
	rootCmd.Flags().StringVarP(&cfg.profileMode, "profile", "p", "none", "enable profiling with pprof. Mode:  none or one of: [cpu, mem, mutex, block, trace, http]")
	rootCmd.Flags().StringVarP(&cfg.port, "port", "", "6060", "port for http-mode of pprof")
	rootCmd.Flags().StringVarP(&cfg.startDelay, "delay", "", "0ns", "delay before visor start")

	cfg.restartCtx = restart.CaptureContext()
}

// Execute executes root CLI command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}

func (cfg *runCfg) startProfiler() *runCfg {
	var option func(*profile.Profile)

	switch cfg.profileMode {
	case "none":
		cfg.profileStop = func() {}
		return cfg
	case "http":
		go func() {
			log.Println(http.ListenAndServe(fmt.Sprintf("localhost:%v", cfg.port), nil))
		}()

		cfg.profileStop = func() {}

		return cfg
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

	cfg.profileStop = profile.Start(profile.ProfilePath("./logs/"+cfg.tag), option).Stop

	return cfg
}

func (cfg *runCfg) startLogger() *runCfg {
	cfg.masterLogger = logging.NewMasterLogger()
	cfg.logger = cfg.masterLogger.PackageLogger(cfg.tag)

	if cfg.syslogAddr != "none" {
		hook, err := logrussyslog.NewSyslogHook("udp", cfg.syslogAddr, syslog.LOG_INFO, cfg.tag)
		if err != nil {
			cfg.logger.Error("Unable to connect to syslog daemon:", err)
		} else {
			cfg.masterLogger.AddHook(hook)
			cfg.masterLogger.Out = ioutil.Discard
		}
	}

	if discordWebhookURL := discord.GetWebhookURLFromEnv(); discordWebhookURL != "" {
		hook := discord.NewHook(cfg.tag, discordWebhookURL)
		logging.AddHook(hook)
	}

	return cfg
}

func (cfg *runCfg) readConfig() *runCfg {
	var rdr io.Reader
	var configPath *string

	if !cfg.cfgFromStdin {
		cp := pathutil.FindConfigPath(cfg.args, 0, configEnv, pathutil.VisorDefaults())

		file, err := os.Open(filepath.Clean(cp))
		if err != nil {
			cfg.logger.Fatalf("Failed to open config: %s", err)
		}

		defer func() {
			if err := file.Close(); err != nil {
				cfg.logger.Warnf("Failed to close config file: %v", err)
			}
		}()

		cfg.logger.Infof("Reading config from %v", cp)

		rdr = file
		configPath = &cp
	} else {
		cfg.logger.Info("Reading config from STDIN")
		rdr = bufio.NewReader(os.Stdin)
	}

	raw, err := ioutil.ReadAll(rdr)
	if err != nil {
		cfg.logger.Fatalf("Failed to read config: %v", err)
	}

	if err := json.Unmarshal(raw, &cfg.conf); err != nil {
		cfg.logger.WithField("raw", string(raw)).Fatalf("Failed to decode config: %s", err)
	}

	cfg.logger.Infof("Config: %#v", &cfg.conf)

	cfg.conf.Path = configPath

	return cfg
}

func (cfg *runCfg) runVisor() *runCfg {
	startDelay, err := time.ParseDuration(cfg.startDelay)
	if err != nil {
		cfg.logger.Warnf("Using no visor start delay due to parsing failure: %v", err)

		startDelay = time.Duration(0)
	}

	if startDelay != 0 {
		cfg.logger.Infof("Visor start delay is %v, waiting...", startDelay)
	}

	time.Sleep(startDelay)

	if cfg.conf.DmsgPty != nil {
		if err := visor.UnlinkSocketFiles(cfg.conf.DmsgPty.CLIAddr); err != nil {
			cfg.logger.Fatal("failed to unlink socket files: ", err)
		}
	}

	vis, err := visor.NewVisor(&cfg.conf, cfg.masterLogger, cfg.restartCtx)
	if err != nil {
		cfg.logger.Fatal("Failed to initialize visor: ", err)
	}

	if cfg.conf.UptimeTracker != nil {
		uptimeTracker, err := utclient.NewHTTP(cfg.conf.UptimeTracker.Addr, cfg.conf.Keys().PubKey, cfg.conf.Keys().SecKey)
		if err != nil {
			cfg.logger.Error("Failed to connect to uptime tracker: ", err)
		} else {
			ticker := time.NewTicker(1 * time.Second)

			go func() {
				for range ticker.C {
					ctx := context.Background()
					if err := uptimeTracker.UpdateVisorUptime(ctx); err != nil {
						cfg.logger.Error("Failed to update visor uptime: ", err)
					}
				}
			}()
		}
	}

	go func() {
		if err := vis.Start(); err != nil {
			cfg.logger.Fatal("Failed to start visor: ", err)
		}
	}()

	if cfg.conf.ShutdownTimeout == 0 {
		cfg.conf.ShutdownTimeout = defaultShutdownTimeout
	}

	cfg.visor = vis

	return cfg
}

func (cfg *runCfg) stopVisor() *runCfg {
	defer cfg.profileStop()

	if err := cfg.visor.Close(); err != nil {
		if !strings.Contains(err.Error(), "closed") {
			cfg.logger.Fatal("Failed to close visor: ", err)
		}
	}

	return cfg
}

func (cfg *runCfg) waitOsSignals() *runCfg {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT}...)
	<-ch

	go func() {
		select {
		case <-time.After(time.Duration(cfg.conf.ShutdownTimeout)):
			cfg.logger.Fatal("Timeout reached: terminating")
		case s := <-ch:
			cfg.logger.Fatalf("Received signal %s: terminating", s)
		}
	}()

	return cfg
}
