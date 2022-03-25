package commands

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"io/ioutil"
	"net/http"
	_ "net/http/pprof" // nolint:gosec // https://golang.org/doc/diagnostics.html#profiling
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pkg/profile"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/spf13/cobra"
	"github.com/toqueteos/webbrowser"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/restart"
	"github.com/skycoin/skywire/pkg/syslog"
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/visor/hypervisorconfig"
	"github.com/skycoin/skywire/pkg/visor/logstore"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var uiAssets fs.FS
var restartCtx = restart.CaptureContext()

const (
	defaultConfigName    = "skywire-config.json"
	runtimeLogMaxEntries = 300
)

var (
	tag                  string
	syslogAddr           string
	pprofMode            string
	pprofAddr            string
	confPath             string
	stdin                bool
	launchBrowser        bool
	hypervisorUI         bool
	remoteHypervisorPKs  string
	disableHypervisorPKs bool
	stopVisorFn          func() // nolint:unused
	stopVisorWg          sync.WaitGroup
)

var rootCmd = &cobra.Command{
	Use:   "skywire-visor",
	Short: "Skywire Visor",
	Run: func(_ *cobra.Command, args []string) {
		runApp(args...)
	},
	Version: buildinfo.Version(),
}

func init() {
	rootCmd.Flags().SortFlags = false
	rootCmd.AddCommand(completionCmd)
	rootCmd.Flags().StringVarP(&confPath, "config", "c", "", "config file to use")
	rootCmd.Flags().BoolVarP(&hypervisorUI, "-hvui", "i", false, "run as hypervisor")
	rootCmd.Flags().BoolVarP(&launchBrowser, "launch-browser", "b", false, "open hypervisor ui in default web browser")
	rootCmd.Flags().StringVarP(&remoteHypervisorPKs, "hv", "j", "", "add remote hypervisor PKs at runtime")
	rootCmd.Flags().BoolVarP(&disableHypervisorPKs, "no-rhv", "k", false, "disable remote hypervisors set in config file")
	rootCmd.Flags().BoolVarP(&stdin, "stdin", "n", false, "read config from stdin")
	rootCmd.Flags().StringVarP(&pprofMode, "pprofmode", "p", "", "pprof mode: cpu, mem, mutex, block, trace, http")
	rootCmd.Flags().StringVarP(&pprofAddr, "pprofaddr", "q", "localhost:6060", "pprof http port")
	rootCmd.Flags().StringVarP(&tag, "tag", "t", "skywire", "logging tag")
	rootCmd.Flags().StringVarP(&syslogAddr, "syslog", "y", "", "syslog server address. E.g. localhost:514")
	extraFlags()
}

func runVisor(args []string) {
	var ok bool
	log := initLogger(tag, syslogAddr)
	store, hook := logstore.MakeStore(runtimeLogMaxEntries)
	log.AddHook(hook)
	if stdin {
		confPath = visorconfig.StdinName
	}
	if _, err := buildinfo.Get().WriteTo(log.Out); err != nil {
		log.WithError(err).Error("Failed to output build info.")
	}

	stopPProf := initPProf(log, tag, pprofMode, pprofAddr)
	defer stopPProf()

	conf := initConfig(log, args, confPath)

	if disableHypervisorPKs {
		conf.Hypervisors = []cipher.PubKey{}
	}

	if remoteHypervisorPKs != "" {
		hypervisorPKsSlice := strings.Split(remoteHypervisorPKs, ",")
		for _, pubkeyString := range hypervisorPKsSlice {
			pubkey := cipher.PubKey{}
			if err := pubkey.Set(pubkeyString); err != nil {
				log.Warnf("Cannot add %s PK as remote hypervisor PK due to: %s", pubkeyString, err)
				continue
			}
			log.Infof("%s PK added as remote hypervisor PK", pubkeyString)
			conf.Hypervisors = append(conf.Hypervisors, pubkey)
		}
	}

	vis, ok := visor.NewVisor(conf, restartCtx)
	if !ok {
		log.Errorln("Failed to start visor.")
		quitSystray()
		return
	}
	vis.SetLogstore(store)

	if launchBrowser {
		runBrowser(conf, log)
	}

	ctx, cancel := cmdutil.SignalContext(context.Background(), log)

	setStopFunction(log, cancel, vis.Close)

	// Wait.
	<-ctx.Done()

	stopVisorFn()
}

// Execute executes root CLI command.
func Execute(ui embed.FS) {
	uiFS, err := fs.Sub(ui, "static")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	uiAssets = uiFS

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
	//var services visorconfig.Services
//	/*
	var r io.Reader

	switch confPath {
	case visorconfig.StdinName:
		log.Info("Reading config from STDIN.")
		r = os.Stdin
	case "":
		// TODO: More robust solution.
		for _, arg := range args {
			if strings.HasSuffix(arg, ".json") {
				confPath = arg
				break
			}
		}

		if confPath == "" {
			confPath = defaultConfigName
		}

		fallthrough
	default:
		log.WithField("filepath", confPath).Info("Reading config from file.")
		f, err := os.Open(confPath) //nolint:gosec
		if err != nil {
			log.WithError(err).
				WithField("filepath", confPath).
				Fatal("Failed to read config file.")
		}
		defer func() { //nolint
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

// 
	conf := initConfig(mLog, args, confPath)

	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(conf); err != nil {
		log.WithError(err).Fatal("Failed to decode config.")
	}

//	conf, err := visorconfig.Parse(mLog, confPath, raw)
//	if err != nil {
//		log.WithError(err).Fatal("Failed to parse config.")
//	}

	if hypervisorUI {
		config := hypervisorconfig.GenerateWorkDirConfig(false)
		conf.Hypervisor = &config
	}

	if conf.Hypervisor != nil {
		conf.Hypervisor.UIAssets = uiAssets
	}

	return conf
}

// runBrowser is actually not recommended because browser shouldn't run as root
// but the vpn Client and Server need root to function.
// Visor also needs write permissions in /opt/skywire
// Consider running the visor at the user level and elevate to run the vpn client and server?
func runBrowser(conf *visorconfig.V1, log *logging.MasterLogger) {
	if conf.Hypervisor == nil {
		log.Errorln("Hypervisor not started - cannot start browser with a regular visor")
		return
	}
	addr := conf.Hypervisor.HTTPAddr
	if addr[0] == ':' {
		addr = "localhost" + addr
	}
	if addr[:4] != "http" {
		if conf.Hypervisor.EnableTLS {
			addr = "https://" + addr
		} else {
			addr = "http://" + addr
		}
	}
	go func() {
		if !checkHvIsRunning(addr, 5) {
			log.Error("Cannot open hypervisor in browser: status check failed")
			return
		}
		if err := webbrowser.Open(addr); err != nil {
			log.WithError(err).Error("webbrowser.Open failed")
		}
	}()
}

func checkHvIsRunning(addr string, retries int) bool {
	url := addr + "/api/ping"
	for i := 0; i < retries; i++ {
		time.Sleep(500 * time.Millisecond)
		resp, err := http.Get(url) // nolint: gosec
		if err != nil {
			continue
		}
		err = resp.Body.Close()
		if err != nil {
			continue
		}
		if resp.StatusCode < 400 {
			return true
		}
	}
	return false
}
