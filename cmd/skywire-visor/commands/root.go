package commands

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	_ "net/http/pprof" // nolint:gosec // https://golang.org/doc/diagnostics.html#profiling
	"os"
	"os/user"
	"strings"
	"sync"

	cc "github.com/ivanpirog/coloredcobra"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/restart"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor"
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
	hypervisorUI         bool
	remoteHypervisorPKs  string
	disableHypervisorPKs bool
	stopVisorFn          func()
	stopVisorWg          sync.WaitGroup
	completion           string
	hiddenflags          []string
	all                  bool
	pkg                  bool
	usr                  bool
	// root indicates process is run with root permissions
	root bool // nolint:unused
	// visorBuildInfo holds information about the build
	visorBuildInfo *buildinfo.Info
)

func init() {
	usrLvl, err := user.Current()
	if err != nil {
		panic(err)
	}
	if usrLvl.Username == "root" {
		root = true
	}

	rootCmd.Flags().SortFlags = false

	rootCmd.Flags().StringVarP(&confPath, "config", "c", "", "config file to use (default): "+skyenv.ConfigName)
	rootCmd.Flags().BoolVarP(&hypervisorUI, "hvui", "i", false, "run as hypervisor")
	rootCmd.Flags().StringVarP(&remoteHypervisorPKs, "hv", "j", "", "add remote hypervisor PKs at runtime")
	hiddenflags = append(hiddenflags, "hv")
	rootCmd.Flags().BoolVarP(&disableHypervisorPKs, "xhv", "k", false, "disable remote hypervisors set in config file")
	hiddenflags = append(hiddenflags, "xhv")
	rootCmd.Flags().BoolVarP(&stdin, "stdin", "n", false, "read config from stdin")
	hiddenflags = append(hiddenflags, "stdin")
	if root {
		if _, err := os.Stat(skyenv.SkywirePath + "/" + skyenv.Configjson); err == nil {
			rootCmd.Flags().BoolVarP(&pkg, "pkg", "p", false, "use package config "+skyenv.SkywirePath+"/"+skyenv.Configjson)
			hiddenflags = append(hiddenflags, "pkg")
		}
	}
	if !root {
		if _, err := os.Stat(skyenv.HomePath() + "/" + skyenv.ConfigName); err == nil {
			rootCmd.Flags().BoolVarP(&usr, "user", "u", false, "use config at: $HOME/"+skyenv.ConfigName)
		}
	}
	rootCmd.Flags().StringVarP(&pprofMode, "pprofmode", "q", "", "pprof mode: cpu, mem, mutex, block, trace, http")
	hiddenflags = append(hiddenflags, "pprofmode")
	rootCmd.Flags().StringVarP(&pprofAddr, "pprofaddr", "r", "localhost:6060", "pprof http port")
	hiddenflags = append(hiddenflags, "pprofaddr")
	rootCmd.Flags().StringVarP(&tag, "tag", "t", "skywire", "logging tag")
	hiddenflags = append(hiddenflags, "tag")
	rootCmd.Flags().StringVarP(&syslogAddr, "syslog", "y", "", "syslog server address. E.g. localhost:514")
	hiddenflags = append(hiddenflags, "syslog")
	rootCmd.Flags().StringVarP(&completion, "completion", "z", "", "[ bash | zsh | fish | powershell ]")
	hiddenflags = append(hiddenflags, "completion")
	rootCmd.Flags().BoolVar(&all, "all", false, "show all flags")

	for _, j := range hiddenflags {
		rootCmd.Flags().MarkHidden(j) //nolint
	}
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
		//log for initial checks
		mLog := initLogger(tag, syslogAddr)
		log := mLog.PackageLogger("pre-run")

		if !stdin {
			//error on multiple configs from flags
			if (pkg && usr) || ((pkg || usr) && (confPath != "")) {
				fmt.Println("Error: multiple configs specified")
				os.Exit(1)
			}
			//use package config
			if pkg {
				confPath = skyenv.SkywirePath + "/" + skyenv.Configjson
			}
			if usr {
				confPath = skyenv.HomePath() + "/" + skyenv.ConfigName
			}
			if confPath == "" {
				confPath = skyenv.ConfigName
			}
			//enforce .json extension
			if !strings.HasSuffix(confPath, ".json") {
				//append .json
				confPath = confPath + ".json"
			}
			//check for the config file
			if _, err := os.Stat(confPath); err != nil {
				//fail here on no config
				log.WithError(err).Fatal("config file not found")
				os.Exit(1)
			}
		} else {
			confPath = visorconfig.StdinName
		}
		logBuildInfo(mLog)
	},
	Run: func(_ *cobra.Command, _ []string) {
		runApp()
	},
	Version: buildinfo.Version(),
}

func runApp() {
	runVisor(nil)
}

// setStopFunction sets the stop function
func setStopFunction(log *logging.MasterLogger, cancel context.CancelFunc, fn func() error) {
	stopVisorWg.Add(1)
	defer stopVisorWg.Done()

	stopVisorFn = func() {
		if err := fn(); err != nil {
			log.WithError(err).Error("Visor closed with error.")
		}
		cancel()
		stopVisorWg.Wait()
	}
}

func runVisor(conf *visorconfig.V1) {
	var ok bool
	log := initLogger(tag, syslogAddr)
	store, hook := logstore.MakeStore(runtimeLogMaxEntries)
	log.AddHook(hook)

	stopPProf := initPProf(log, tag, pprofMode, pprofAddr)
	defer stopPProf()

	if conf == nil {
		conf = initConfig(log, confPath)
	}

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

	ctx, cancel := cmdutil.SignalContext(context.Background(), log)
	vis, ok := visor.NewVisor(ctx, conf, restartCtx)
	if !ok {
		select {
		case <-ctx.Done():
			log.Info("Visor closed early.")
		default:
			log.Errorln("Failed to start visor.")
		}
		return
	}

	setStopFunction(log, cancel, vis.Close)

	vis.SetLogstore(store)

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

func logBuildInfo(mLog *logging.MasterLogger) {
	log := mLog.PackageLogger("buildinfo")
	visorBuildInfo = buildinfo.Get()
	if visorBuildInfo.Version != "unknown" {
		log.WithField(" version", visorBuildInfo.Version).WithField("built on", visorBuildInfo.Date).WithField("commit", visorBuildInfo.Commit).Info()
	}
}
