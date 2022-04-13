package commands

import (
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
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/bitfield/script"
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
	stopVisorFn          func()
	stopVisorWg          sync.WaitGroup
	completion           string
	hiddenflags          []string
	all                  bool
	pkg                  bool
	pkg1                 bool

	// skywire is the path to the running visor binary
	skywire string
	// workDir is the working directory where skywire-visor was executed
	workDir string // nolint:unused
	// root indicates process is run with root permissions
	root bool // nolint:unused
	// visorBuildInfo holds information about the build
	visorBuildInfo *buildinfo.Info
)

func init() {
	thisUser, err := user.Current()
	if err != nil {
		panic(err)
	}
	if thisUser.Username == "root" {
		root = true
	}

	rootCmd.Flags().SortFlags = false

	rootCmd.Flags().StringVarP(&confPath, "config", "c", skyenv.ConfigName, "config file to use")
	rootCmd.Flags().BoolVarP(&hypervisorUI, "hvui", "i", false, "run as hypervisor")
	if ((skyenv.OS == "linux") && !root) || ((skyenv.OS == "mac") && !root) || (skyenv.OS == "win") {
		rootCmd.Flags().BoolVarP(&launchBrowser, "browser", "b", false, "open hypervisor ui in default web browser")
	}
	rootCmd.Flags().StringVarP(&remoteHypervisorPKs, "hv", "j", "", "add remote hypervisor PKs at runtime")
	hiddenflags = append(hiddenflags, "hv")
	rootCmd.Flags().BoolVarP(&disableHypervisorPKs, "xhv", "k", false, "disable remote hypervisors set in config file")
	hiddenflags = append(hiddenflags, "xhv")
	rootCmd.Flags().BoolVarP(&stdin, "stdin", "n", false, "read config from stdin")
	hiddenflags = append(hiddenflags, "stdin")
	if skyenv.OS == "linux" {
		rootCmd.Flags().BoolVar(&pkg, "ph", false, "use package config "+skyenv.SkywirePath+"/"+skyenv.Skywirejson)
		hiddenflags = append(hiddenflags, "ph")
		rootCmd.Flags().BoolVar(&pkg1, "pv", false, "use package config "+skyenv.SkywirePath+"/"+skyenv.Skywirevisorjson)
		hiddenflags = append(hiddenflags, "pv")
	}
	rootCmd.Flags().StringVarP(&pprofMode, "pprofmode", "p", "", "pprof mode: cpu, mem, mutex, block, trace, http")
	hiddenflags = append(hiddenflags, "pprofmode")
	rootCmd.Flags().StringVarP(&pprofAddr, "pprofaddr", "q", "localhost:6060", "pprof http port")
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
		log := initLogger(tag, syslogAddr)
		_, hook := logstore.MakeStore(runtimeLogMaxEntries)
		log.AddHook(hook)
		if !stdin {
			//multiple configs from flags
			if (pkg && pkg1) || ((pkg || pkg1) && (confPath != "")) {
				fmt.Println("Error: multiple configs specified")
				os.Exit(1)
			}
			//use package hypervisor config
			if pkg {
				confPath = skyenv.SkywirePath + "/" + skyenv.Skywirejson
			}
			//use package visor config
			if pkg1 {
				confPath = skyenv.SkywirePath + "/" + skyenv.Skywirevisorjson
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
		var fork string
		var branch string
		//indicates how skywire was started
		skywire = os.Args[0]
		//indicates where skywire was started
		path, err := os.Getwd()
		if err != nil {
			log.WithError(err).Fatal()
		}
		workDir = path
		//retrieve build info
		visorBuildInfo = buildinfo.Get()
		if visorBuildInfo.Version == "unknown" {
			if match := strings.Contains("/tmp/", skywire); err == nil {
				if match {
					log.Info("executed with go run")
					log.WithField("binary: ", skywire).Info()
				}
			}
			//check for .git folder for versioning
			if _, err := os.Stat(".git"); err == nil {
				//attempt to version from git sources
				if _, err = exec.LookPath("git"); err == nil {
					if version, err := script.Exec(`git describe`).String(); err == nil {
						visorBuildInfo.Version = strings.ReplaceAll(version, "\n", "")
						if visorBuildInfo.Commit == "unknown" {
							if commit, err := script.Exec(`git rev-list -1 HEAD`).String(); err == nil {
								visorBuildInfo.Commit = strings.ReplaceAll(commit, "\n", "")
							}
						}
						if fork, err = script.Exec(`git config --get remote.origin.url`).String(); err == nil {
							fork = strings.ReplaceAll(fork, "ssh://", "")
							fork = strings.ReplaceAll(fork, "git@", "")
							fork = strings.ReplaceAll(fork, "https://", "")
							fork = strings.ReplaceAll(fork, "http://", "")
							fork = strings.ReplaceAll(fork, "github.com/", "")
							fork = strings.ReplaceAll(fork, ":/", "")
							fork = strings.ReplaceAll(fork, "\n", "")
							nofork, err := regexp.MatchString("skycoin/skywire", fork)
							if err != nil {
								log.Error(err)
							} else {
								log.Info(nofork)
								if !nofork {
									fork = ""
								}
							}
						}
						if branch, err = script.Exec(`git rev-parse --abbrev-ref HEAD`).String(); err == nil {
							branch = strings.ReplaceAll(branch, "\n", "")
							if _, err = exec.LookPath("date"); err == nil {
								if visorBuildInfo.Date == "unknown" {
									if date, err := script.Exec(`date -u +%Y-%m-%dT%H:%M:%SZ`).String(); err == nil {
										visorBuildInfo.Date = strings.ReplaceAll(date, "\n", "")
									}
								}
							}
						}
					}
				}
			}
		}
		log.WithField("version: ", visorBuildInfo.Version).Info()
		if visorBuildInfo.Date != "unknown" && visorBuildInfo.Date != "" {
			log.WithField("built on: ", visorBuildInfo.Date).Info()
		}
		if visorBuildInfo.Commit != "unknown" && visorBuildInfo.Commit != "" {
			log.WithField("against commit: ", visorBuildInfo.Commit).Info()
			if fork != "" {
				log.WithField("fork: ", fork).Info()
			}
		}
		if branch != "unknown" && branch != "" {
			log.WithField("branch: ", branch).Info()
		}
	},
	Run: func(_ *cobra.Command, _ []string) {
		runApp()
	},
	Version: buildinfo.Version(),
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

	//dont create files & directories as root in non root-owned dir
	if _, err := exec.LookPath("stat"); err == nil {
		if owner, err := script.Exec(`stat -c '%U' ` + conf.LocalPath + "/..").String(); err == nil {
			if ((owner != "root") || (owner != "root\n")) && root {
				log.WithField("local path: ", conf.LocalPath).Error()
				log.Fatal("not writing as root to local path not owned by root")
			}
			//similarly, anticipate and fail on the reverse instance
			if ((owner == "root") || (owner == "root\n")) && !root {
				log.WithField("local path: ", conf.LocalPath).Error()
				log.Fatal("Insufficient permissions to write to the local path")
			}
		}
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
		log.Info("Reading config from file.")
		log.WithField("filepath", confPath).Info()
		f, err := os.ReadFile(filepath.Clean(confPath))
		if err != nil {
			log.WithError(err).Fatal("Failed to read config file.")
		}
		r = bytes.NewReader(f)
	}

	conf, compat, err := visorconfig.Parse(log, r, confPath, visorBuildInfo)
	if err != nil {
		log.WithError(err).Fatal("Failed to read in config.")
	}

	if !compat {
		log.Error("config version does not match visor version")
		log.WithField("skywire version: ", visorBuildInfo.Version).Error()
		var updstr string
		if match := strings.Contains("/tmp/", skywire); err == nil {
			log.Info("match:", match)
			if match {
				if _, err := os.Stat("cmd/skywire-cli/skywire-cli.go"); err == nil {
					updstr = "go run cmd/skywire-cli/skywire-cli.go config gen -b"
				}
				log.Info("updstr:", updstr)

			}
		}
		if updstr == "" {
			updstr = "skywire-cli config gen -b"
		}
		if conf.Hypervisor != nil {
			updstr = updstr + "i"
		}
		for _, j := range conf.Hypervisors {
			if fmt.Sprintf("\t%s\n", j) != "" {
				updstr = updstr + "x"
				break
			}
		}

		if pkgenv := strings.Contains("/opt/skywire/apps", conf.Launcher.BinPath); err == nil {
			if pkgenv {
				updstr = updstr + "p"
			}
		}
		//there is no config *file* with stdin
		if confPath != visorconfig.StdinName {
			if _, err = exec.LookPath("stat"); err == nil {
				if owner, err := script.Exec(`stat -c '%U' ` + confPath).String(); err == nil {
					if (owner == "root") || (owner == "root\n") {
						updstr = "sudo " + updstr
					}
				}
			}
			updstr = "\n		" + updstr + "ro " + confPath + "\n"
		} else {
			updstr = "\n		" + updstr + "n" + " | go run cmd/skywire-visor/skywire-visor.go -n"
			if launchBrowser {
				updstr = updstr + "b"
			}
			updstr = updstr + "\n"
		}
		updstr = "\n		" + updstr + "\n"
		log.Info("please update your config with the following command:\n", updstr)
		log.Fatal("failed to start skywire")
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
