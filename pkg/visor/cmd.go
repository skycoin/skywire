// Package visor implements skywire visor.
package visor

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	_ "net/http/pprof" // nolint:gosec // https://golang.org/doc/diagnostics.html#profiling
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bitfield/script"
	"github.com/pkg/profile"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/restart"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/hypervisorconfig"
	"github.com/skycoin/skywire/pkg/visor/logstore"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var uiAssets fs.FS

var (
	restartCtx           = restart.CaptureContext()
	isAutoPeer           bool
	autoPeerIP           string
	stopVisorWg          sync.WaitGroup //nolint:unused
	launchBrowser        bool
	syslogAddr           string
	logger               = logging.MustGetLogger("skywire-visor")
	logLvl               string
	pprofMode            string
	pprofAddr            string
	confPath             string
	stdin                bool
	hypervisorUI         bool
	noHypervisorUI       bool
	remoteHypervisorPKs  string
	disableHypervisorPKs bool
	completion           string
	logTag               string
	hiddenflags          []string
	all                  bool
	pkg                  bool
	usr                  bool
	localIPs             []net.IP //  nolint:unused
	runAsSystray         bool
	// root indicates process is run with root permissions
	root bool // nolint:unused
	// visorBuildInfo holds information about the build
	visorBuildInfo *buildinfo.Info
)

func init() {
	root = skyenv.IsRoot()

	var ui embed.FS
	uiFS, err := fs.Sub(ui, "static")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	uiAssets = uiFS
	RootCmd.Flags().SortFlags = false
	//the default is not set to fix the aesthetic of the help command
	RootCmd.Flags().StringVarP(&confPath, "config", "c", "", "config file to use (default): "+skyenv.ConfigName)
	if ((skyenv.OS == "linux") && !root) || ((skyenv.OS == "mac") && !root) || (skyenv.OS == "win") {
		RootCmd.Flags().BoolVarP(&launchBrowser, "browser", "b", false, "open hypervisor ui in default web browser")
	}
	RootCmd.Flags().BoolVarP(&stdin, "stdin", "n", false, "read config from stdin")
	hiddenflags = append(hiddenflags, "stdin")
	//only show flags for configs which exist
	if root {
		if _, err := os.Stat(skyenv.SkywirePath + "/" + skyenv.ConfigJSON); err == nil {
			RootCmd.Flags().BoolVarP(&pkg, "pkg", "p", false, "use package config "+skyenv.SkywirePath+"/"+skyenv.ConfigJSON)
			hiddenflags = append(hiddenflags, "pkg")
		}
	}
	if !root {
		if _, err := os.Stat(skyenv.HomePath() + "/" + skyenv.ConfigName); err == nil {
			RootCmd.Flags().BoolVarP(&usr, "user", "u", false, "use config at: $HOME/"+skyenv.ConfigName)
		}
	}
	RootCmd.Flags().BoolVar(&runAsSystray, "systray", false, "run as systray")
	RootCmd.Flags().BoolVarP(&hypervisorUI, "hvui", "i", false, "run as hypervisor \u001b[0m*")
	RootCmd.Flags().BoolVarP(&noHypervisorUI, "nohvui", "x", false, "disable hypervisor \u001b[0m*")
	hiddenflags = append(hiddenflags, "nohvui")
	RootCmd.Flags().StringVarP(&remoteHypervisorPKs, "hv", "j", "", "add remote hypervisor \u001b[0m*")
	hiddenflags = append(hiddenflags, "hv")
	RootCmd.Flags().BoolVarP(&disableHypervisorPKs, "xhv", "k", false, "disable remote hypervisors \u001b[0m*")
	hiddenflags = append(hiddenflags, "xhv")
	if os.Getenv("SKYBIAN") == "true" {
		initAutoPeerFlags()
	}
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "s", "", "[ debug | warn | error | fatal | panic | trace ] \u001b[0m*")
	hiddenflags = append(hiddenflags, "loglvl")
	RootCmd.Flags().StringVarP(&pprofMode, "pprofmode", "q", "", "[ cpu | mem | mutex | block | trace | http ]")
	hiddenflags = append(hiddenflags, "pprofmode")
	RootCmd.Flags().StringVarP(&pprofAddr, "pprofaddr", "r", "localhost:6060", "pprof http port")
	hiddenflags = append(hiddenflags, "pprofaddr")
	RootCmd.Flags().StringVarP(&logTag, "logtag", "t", "skywire", "logging tag")
	hiddenflags = append(hiddenflags, "logtag")
	RootCmd.Flags().StringVarP(&syslogAddr, "syslog", "y", "", "syslog server address. E.g. localhost:514")
	hiddenflags = append(hiddenflags, "syslog")
	RootCmd.Flags().StringVarP(&completion, "completion", "z", "", "[ bash | zsh | fish | powershell ]")
	hiddenflags = append(hiddenflags, "completion")
	RootCmd.Flags().BoolVar(&all, "all", false, "show all flags")

	for _, j := range hiddenflags {
		RootCmd.Flags().MarkHidden(j) //nolint
	}
}
func initAutoPeerFlags() {
	localIPs, err := netutil.DefaultNetworkInterfaceIPs()
	if err != nil {
		logger.WithError(err).Warn("Could not determine network interface IP address")
		if len(localIPs) == 0 {
			localIPs = append(localIPs, net.ParseIP("192.168.0.1"))
		}
	}
	RootCmd.Flags().StringVarP(&autoPeerIP, "hvip", "l", trimStringFromDot(localIPs[0].String())+".2:7998", "set hypervisor by ip")
	hiddenflags = append(hiddenflags, "hvip")
	isDefaultAutopeer := false
	if os.Getenv("AUTOPEER") == "1" {
		isDefaultAutopeer = true
	}
	RootCmd.Flags().BoolVarP(&isAutoPeer, "autopeer", "m", isDefaultAutopeer, "enable autopeering")
	hiddenflags = append(hiddenflags, "autopeer")
}
func trimStringFromDot(s string) string {
	if idx := strings.LastIndex(s, "."); idx != -1 {
		return s[:idx]
	}
	return s
}

// RootCmd contains the help command & invocation flags
var RootCmd = &cobra.Command{
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
			cmd.Flags().MarkHidden("all")  //nolint
			cmd.Flags().MarkHidden("help") //nolint
			cmd.Help()                     //nolint
			fmt.Println("                            * \u001b[94moverrides config file\u001b[0m")
			os.Exit(0)
		}
		// -z --completion
		genCompletion(cmd)
		//log for initial checks
		mLog := initLogger()
		log := mLog.PackageLogger("pre-run")

		if stdin {
			confPath = visorconfig.StdinName
		} else {
			//error on multiple configs from flags
			if (pkg && usr) || ((pkg || usr) && (confPath != "")) {
				fmt.Println("Error: multiple configs specified")
				os.Exit(1)
			}
			//use package config /opt/skywire/skywire.json
			if pkg {
				confPath = skyenv.SkywirePath + "/" + skyenv.ConfigJSON
			}
			//userspace config in $HOME/.skywire/skywire-config.json
			if usr {
				confPath = skyenv.HomePath() + "/" + skyenv.ConfigName
			}
			if confPath == "" {
				//default config in current dir ./skywire-config.json
				confPath = skyenv.ConfigName
			}
			//enforce .json extension
			if !strings.HasSuffix(confPath, ".json") {
				confPath = confPath + ".json"
			}
			//check for the config file
			if _, err := os.Stat(confPath); err != nil {
				//fail here on no config
				log.WithError(err).Fatal("config file not found")
				os.Exit(1)
			}
		}
		logBuildInfo(mLog)
		if launchBrowser {
			hypervisorUI = true
		}
		//warn about creating files & directories as root in non root-owned dir
		if skyenv.OS == "linux" {
			//`stat` command on linux will give file ownership whereas os.Stat() does not
			if _, err := exec.LookPath("stat"); err == nil {
				c, _ := filepath.Split(confPath)
				if c == "" {
					c = "./"
				}
				owner, err := script.Exec(`stat -c '%U' ` + c).String()
				if err != nil {
					log.Error("cannot stat: " + c)
				}
				rootOwner, err := script.Exec(`stat -c '%U' /root`).String()
				if err != nil {
					log.Error("cannot stat: /root")
				}
				if (owner != rootOwner) && root {
					log.Warn("writing as root to directory not owned by root")
				}
				if !root && (owner == rootOwner) {
					log.Fatal("Insufficient permissions to write to the specified path")
				}
			}
		}
		if runAsSystray {
			if skyenv.OS == "linux" {
				if root {
					log.Warn("Systray cannot start in userspace when visor is run as root")
				}
			}
		}

	},
	Run: func(_ *cobra.Command, _ []string) {
		if runAsSystray {
			runAppSystray()
		} else {
			runApp()
		}
	},
	Version: buildinfo.Version(),
}

func runVisor(conf *visorconfig.V1) {
	//var ok bool
	log := initLogger()
	_, hook := logstore.MakeStore(runtimeLogMaxEntries)
	log.AddHook(hook)

	stopPProf := initPProf(log, pprofMode, pprofAddr)
	defer stopPProf()

	if conf == nil {
		conf = initConfig(log, confPath)
	}

	if disableHypervisorPKs {
		conf.Hypervisors = []cipher.PubKey{}
	}

	pubkey := cipher.PubKey{}
	if remoteHypervisorPKs != "" {
		hypervisorPKsSlice := strings.Split(remoteHypervisorPKs, ",")
		for _, pubkeyString := range hypervisorPKsSlice {
			if err := pubkey.Set(pubkeyString); err != nil {
				log.Warnf("Cannot add %s PK as remote hypervisor PK due to: %s", pubkeyString, err)
				continue
			}
			log.Infof("%s PK added as remote hypervisor PK", pubkeyString)
			conf.Hypervisors = append(conf.Hypervisors, pubkey)
		}
	}
	//autopeering should only happen when there is no local or remote hypervisor set in the config.
	if isAutoPeer && conf.Hypervisor != nil {
		log.Info("Local hypervisor running, disabling autopeer")
		isAutoPeer = false
	}

	if isAutoPeer && len(conf.Hypervisors) > 0 {
		log.Info("%d Remote hypervisor(s) set in config; disabling autopeer", len(conf.Hypervisors))
		log.Info(conf.Hypervisors)
		isAutoPeer = false
	}

	if isAutoPeer {
		log.Info("Autopeer: ", isAutoPeer)
		hvkey, err := FetchHvPk(autoPeerIP)
		if err != nil {
			log.WithError(err).Error("Failure autopeering - unable to obtain hypervisor public key")
		} else {
			hvkey = strings.TrimSpace(hvkey)
			hypervisorPKsSlice := strings.Split(hvkey, ",")
			for _, pubkeyString := range hypervisorPKsSlice {
				if err := pubkey.Set(pubkeyString); err != nil {
					log.Warnf("Cannot add %s PK as remote hypervisor PK due to: %s", pubkeyString, err)
					continue
				}
				log.Infof("%s PK added as remote hypervisor PK", pubkeyString)
				conf.Hypervisors = append(conf.Hypervisors, pubkey)
			}
		}
	}
	if logLvl != "" {
		//validate & set log level
		_, err := logging.LevelFromString(logLvl)
		if err != nil {
			log.WithError(err).Error("Invalid log level specified: ", logLvl)
		} else {
			conf.LogLevel = logLvl
			log.Info("setting log level to: ", logLvl)
		}
	}

	RunVisor(conf, uiAssets)

}

func initPProf(log *logging.MasterLogger, profMode string, profAddr string) (stop func()) {
	var optFunc func(*profile.Profile)

	switch profMode {
	case "none", "":
	case "http":
		go func() {
			srv := &http.Server{ //nolint gosec
				Addr:         profAddr,
				Handler:      nil,
				ReadTimeout:  5 * time.Second,
				WriteTimeout: 10 * time.Second,
			}
			err := srv.ListenAndServe()
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
		stop = profile.Start(profile.ProfilePath("./logs/"+logTag), optFunc).Stop
	}

	if stop == nil {
		stop = func() {}
	}
	return stop
}

func initConfig(mLog *logging.MasterLogger, confPath string) *visorconfig.V1 { //nolint
	log := mLog.PackageLogger("visor:config")

	var r io.Reader

	switch confPath {
	case visorconfig.StdinName:
		log.Info("Reading config from STDIN.")
		r = os.Stdin
	case "":
		fallthrough
	default:
		log.WithField("filepath", confPath).Info()
		f, err := os.ReadFile(filepath.Clean(confPath))
		if err != nil {
			log.WithError(err).Fatal("Failed to read config file.")
		}
		confPath = filepath.Clean(confPath)
		r = bytes.NewReader(f)
	}

	conf, compat, err := visorconfig.Parse(log, r, confPath, visorBuildInfo)
	if err != nil {
		log.WithError(err).Fatal("Failed to read in config.")
	}
	if !compat {
		log.Fatalf("failed to start skywire - config version is incompatible")
	}
	if hypervisorUI {
		config := hypervisorconfig.GenerateWorkDirConfig(false)
		conf.Hypervisor = &config
	}
	if conf.Hypervisor != nil {
		conf.Hypervisor.UIAssets = uiAssets
	}
	if noHypervisorUI {
		conf.Hypervisor = nil
	}

	skyenv.VisorConfigFile = confPath
	return conf
}

func logBuildInfo(mLog *logging.MasterLogger) {
	log := mLog.PackageLogger("buildinfo")
	visorBuildInfo = buildinfo.Get()
	if visorBuildInfo.Version != "unknown" {
		log.WithField(" version", visorBuildInfo.Version).WithField("built on", visorBuildInfo.Date).WithField("commit", visorBuildInfo.Commit).Info()
	}
}

func genCompletion(cmd *cobra.Command) {
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

}
