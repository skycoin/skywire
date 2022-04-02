package commands

import (
//	"bufio"
	"bytes"
	"context"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"io/ioutil"
	"net/http"
	_ "net/http/pprof" // nolint:gosec // https://golang.org/doc/diagnostics.html#profiling
	"os"
	//"path/filepath"
	"strings"
	"sync"
	"time"

	cc "github.com/ivanpirog/coloredcobra"
	"github.com/pkg/profile"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/spf13/cobra"
	"github.com/toqueteos/webbrowser"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/restart"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/syslog"
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/visor/hypervisorconfig"
	"github.com/skycoin/skywire/pkg/visor/logstore"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var uiAssets fs.FS
var restartCtx = restart.CaptureContext()

const (
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
	completion           string
	hiddenflags          []string
	all                  bool
	pkg	bool
	pkg1	bool
)

func init() {
	rootCmd.Flags().SortFlags = false

	rootCmd.Flags().StringVarP(&confPath, "config", "c", "", "config file to use default: "+skyenv.ConfigName)
	rootCmd.Flags().BoolVarP(&hypervisorUI, "hvui", "i", false, "run as hypervisor")
	rootCmd.Flags().BoolVarP(&launchBrowser, "browser", "b", false, "open hypervisor ui in default web browser")
	rootCmd.Flags().StringVarP(&remoteHypervisorPKs, "hv", "j", "", "add remote hypervisor PKs at runtime")
	rootCmd.Flags().BoolVarP(&disableHypervisorPKs, "xhv", "k", false, "disable remote hypervisors set in config file")
	rootCmd.Flags().BoolVarP(&stdin, "stdin", "n", false, "read config from stdin")
	rootCmd.Flags().BoolVar(&pkg, "ph", false, "use package config "+skyenv.SkywirePath+"/"+skyenv.Skywirejson)
	rootCmd.Flags().BoolVar(&pkg1, "pv", false, "use package config "+skyenv.SkywirePath+"/"+skyenv.Skywirevisorjson)
	rootCmd.Flags().StringVarP(&pprofMode, "pprofmode", "p", "", "pprof mode: cpu, mem, mutex, block, trace, http")
	rootCmd.Flags().StringVarP(&pprofAddr, "pprofaddr", "q", "localhost:6060", "pprof http port")
	rootCmd.Flags().StringVarP(&tag, "tag", "t", "skywire", "logging tag")
	rootCmd.Flags().StringVarP(&syslogAddr, "syslog", "y", "", "syslog server address. E.g. localhost:514")
	rootCmd.Flags().StringVarP(&completion, "completion", "z", "", "[ bash | zsh | fish | powershell ]")
	rootCmd.Flags().BoolVar(&all, "all", false, "show all flags")

	hiddenflags = []string{"hv", "xhv", "stdin", "pprofmode", "pprofaddr", "tag", "syslog", "completion", "ph", "pv"}
	for _, j := range hiddenflags {
		rootCmd.Flags().MarkHidden(j) //nolint
	}

	extraFlags()
}

var rootCmd = &cobra.Command{
	Use:   "skywire-visor",
	Short: "Skywire Visor",
	Long: `
	┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐
	└─┐├┴┐└┬┘││││├┬┘├┤
	└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘`,
	PreRun: func(cmd *cobra.Command, _ []string) {
		// --all unhide flags and print help menu
		if all {
			for _, j := range hiddenflags {
				f := cmd.Flags().Lookup(j) //nolint
				f.Hidden = false
			}
			cmd.Flags().MarkHidden("all") //nolint
			cmd.Help()                    //nolint
			os.Exit(0)
		}
		// -z --completion
		switch completion {
		case "bash":
			err := cmd.Root().GenBashCompletion(os.Stdout)
			if err != nil {
				panic(err)
			}
		case "zsh":
			err := cmd.Root().GenZshCompletion(os.Stdout)
			if err != nil {
				panic(err)
			}
		case "fish":
			err := cmd.Root().GenFishCompletion(os.Stdout, true)
			if err != nil {
				panic(err)
			}
		case "powershell":
			err := cmd.Root().GenPowerShellCompletion(os.Stdout)
			if err != nil {
				panic(err)
			}
		}
		//error on unrecognized
		if (completion != "bash") && (completion != "zsh") && (completion != "fish") && (completion != "") {
			fmt.Println("Invalid completion specified:", completion)
			os.Exit(1)
		}
		if !stdin {
		//multiple configs from flags
		if (pkg && pkg1) || ((pkg || pkg1) && (confPath != "")) {
			fmt.Println("Error: multiple configurations specified")
			os.Exit(1)
		}
		//use package hypervisor config
		if pkg {
			confPath = skyenv.SkywirePath+"/"+skyenv.Skywirejson
		}
		//use package visor config
		if pkg1 {
			confPath = skyenv.SkywirePath+"/"+skyenv.Skywirevisorjson
		}
		//set skyenv.ConfigName to confPath
		if confPath != "" {
			skyenv.ConfigName = confPath
		}
		//set confPath if unset
		if confPath == "" {
			confPath = skyenv.ConfigName
		}
		//check for the config file
		if strings.HasSuffix(skyenv.ConfigName, ".json") {
			fmt.Println("specified config file:", skyenv.ConfigName)
			} else {
				fmt.Println("file does not have .json extension.")
				fmt.Println("Checking for ", skyenv.ConfigName+".json")
				skyenv.ConfigName = skyenv.ConfigName+".json"
			}
		if _, err := os.Stat(skyenv.ConfigName); err == nil {
			fmt.Println("found config file:", skyenv.ConfigName)
		} else {
			fmt.Println("Invalid configuration specified:", skyenv.ConfigName)
			os.Exit(1)
		}
	} else {
		skyenv.ConfigName = visorconfig.StdinName
	}
	},

	Run: func(_ *cobra.Command, args []string) {
		runApp(args)
	},
	Version: buildinfo.Version(),
}

func runVisor(args []string) {
	var ok bool
	log := initLogger(tag, syslogAddr)
	store, hook := logstore.MakeStore(runtimeLogMaxEntries)
	log.AddHook(hook)

	if _, err := buildinfo.Get().WriteTo(log.Out); err != nil {
		log.WithError(err).Error("Failed to output build info.")
	}

	stopPProf := initPProf(log, tag, pprofMode, pprofAddr)
	defer stopPProf()

	conf := initConfig(log, args)

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
	cc.Init(&cc.Config{
		RootCmd:         rootCmd,
		Headings:        cc.HiBlue + cc.Bold,
		Commands:        cc.HiBlue + cc.Bold,
		CmdShortDescr:   cc.HiBlue,
		Example:         cc.HiBlue + cc.Italic,
		ExecName:        cc.HiBlue + cc.Bold,
		Flags:           cc.HiBlue + cc.Bold,
		FlagsDescr:      cc.HiBlue,
		NoExtraNewlines: true,
		NoBottomNewline: true,
	})
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

func initConfig(mLog *logging.MasterLogger, args []string) *visorconfig.V1 { //nolint
	log := mLog.PackageLogger("visor:config")

		var r io.Reader

		switch skyenv.ConfigName {
		case visorconfig.StdinName:
			log.Info("Reading config from STDIN.")
			r = os.Stdin
		case "":
			fallthrough
		default:
			log.WithField("filepath", skyenv.ConfigName).Info("Reading config from file.")
			    f, err := os.ReadFile(skyenv.ConfigName)
			if err != nil {
				log.WithError(err).
					WithField("filepath", skyenv.ConfigName).
					Fatal("Failed to read config file.")
			}
			r = bytes.NewReader(f)
		}

		conf, err := visorconfig.Reader(r)
		if err != nil {
			log.WithError(err).Fatal("Failed to read in config.")
		}
		fmt.Println(conf.Version)
		if conf.Dmsg.Servers != nil {
			fmt.Println("dmsg servers detected")
	}
	if hypervisorUI {
		config := hypervisorconfig.GenerateWorkDirConfig(false)
		conf.Hypervisor = &config
	}

	if conf.Hypervisor != nil {
		conf.Hypervisor.UIAssets = uiAssets
	}
	return conf
}

// runBrowser opens the hypervisor interface in the browser
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
