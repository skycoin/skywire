// Package visor implements skywire visor.
package visor

import (
	"bytes"
	"fmt"
	"io"
	"net"
	_ "net/http/pprof" // nolint:gosec // https://golang.org/doc/diagnostics.html#profiling
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bitfield/script"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var (
	pkgconfigexists  bool
	userconfigexists bool
	//	isAutoPeer           bool
	//	autoPeerIP           string
	stopVisorWg          sync.WaitGroup //nolint:unused
	launchBrowser        bool
	syslogAddr           string
	logger               = logging.MustGetLogger("skywire-visor") //nolint:unused
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
	useCsrf              bool
	pkg                  bool
	usr                  bool
	localIPs             []net.IP //  nolint:unused
	runAsSystray         bool
	// root indicates process is run with root permissions
	root bool // nolint:unused
	// visorBuildInfo holds information about the build
	visorBuildInfo *buildinfo.Info
	dmsgServer     string
	isStoreLog     bool
	isForceColor   bool
)

func init() {

	root = visorconfig.IsRoot()
	RootCmd.Flags().SortFlags = false
	//the default is not set to fix the aesthetic of the help command
	RootCmd.Flags().StringVarP(&confPath, "config", "c", "", "config file to use (default): "+visorconfig.ConfigName)
	if ((visorconfig.OS == "linux") && !root) || ((visorconfig.OS == "mac") && !root) || (visorconfig.OS == "win") {
		RootCmd.Flags().BoolVarP(&launchBrowser, "browser", "b", false, "open hypervisor ui in default web browser")
	}
	RootCmd.Flags().StringVar(&dmsgServer, "dmsg-server", "", "use specified dmsg server public key")
	hiddenflags = append(hiddenflags, "dmsg-server")
	RootCmd.Flags().BoolVarP(&stdin, "stdin", "n", false, "read config from stdin")
	hiddenflags = append(hiddenflags, "stdin")
	//only show flags for configs which exist

	if _, err := os.Stat(visorconfig.SkywirePath + "/" + visorconfig.ConfigJSON); err == nil {
		pkgconfigexists = true
	}
	if _, err := os.Stat(visorconfig.HomePath() + "/" + visorconfig.ConfigName); err == nil {
		userconfigexists = true
	}
	if root && pkgconfigexists {
		RootCmd.Flags().BoolVarP(&pkg, "pkg", "p", false, "use package config "+visorconfig.SkywirePath+"/"+visorconfig.ConfigJSON)
	}
	if !root && userconfigexists {
		RootCmd.Flags().BoolVarP(&usr, "user", "u", false, "use config at: $HOME/"+visorconfig.ConfigName)
	}
	var reason string
	if RootCmd.Flags().Lookup("user") == nil {
		if !userconfigexists {
			reason = "does not exist"
		} else {
			if root {
				reason = "unusable with current permissions"
			}
		}
		RootCmd.Flags().BoolVarP(&usr, "user", "u", false, "u̶s̶e̶r̶s̶p̶a̶c̶e̶ ̶c̶o̶n̶f̶i̶g̶ "+reason)
	}
	if RootCmd.Flags().Lookup("pkg") == nil {
		if !pkgconfigexists {
			reason = "does not exist"
		} else {
			if !root {
				reason = "requires root permissions"
			}
		}
		RootCmd.Flags().BoolVarP(&pkg, "pkg", "p", false, "p̶a̶c̶k̶a̶g̶e̶ ̶c̶o̶n̶f̶i̶g̶ "+reason)
	}
	hiddenflags = append(hiddenflags, "pkg")
	hiddenflags = append(hiddenflags, "user")
	RootCmd.Flags().BoolVar(&runAsSystray, "systray", false, "run as systray")
	RootCmd.Flags().BoolVarP(&hypervisorUI, "hvui", "i", false, "run as hypervisor \u001b[0m*")
	RootCmd.Flags().BoolVarP(&noHypervisorUI, "nohvui", "x", false, "disable hypervisor \u001b[0m*")
	hiddenflags = append(hiddenflags, "nohvui")
	RootCmd.Flags().StringVarP(&remoteHypervisorPKs, "hv", "j", "", "add remote hypervisor \u001b[0m*")
	hiddenflags = append(hiddenflags, "hv")
	RootCmd.Flags().BoolVarP(&disableHypervisorPKs, "xhv", "k", false, "disable remote hypervisors \u001b[0m*")
	hiddenflags = append(hiddenflags, "xhv")
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
	RootCmd.Flags().BoolVarP(&isStoreLog, "storelog", "l", false, "store all logs to file")
	hiddenflags = append(hiddenflags, "storelog")
	RootCmd.Flags().BoolVar(&isForceColor, "forcecolor", false, "force color logging when out is not STDOUT")
	hiddenflags = append(hiddenflags, "forcecolor")
	RootCmd.Flags().BoolVar(&all, "all", false, "show all flags")
	RootCmd.Flags().BoolVar(&useCsrf, "csrf", true, "Request a CSRF token for sensitive hypervisor API requests")
	for _, j := range hiddenflags {
		RootCmd.Flags().MarkHidden(j) //nolint
	}
	RootCmd.SetUsageTemplate(help)

}

// RootCmd contains the help command & invocation flags
var RootCmd = &cobra.Command{
	Use:   "visor",
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
			fmt.Println("\033[F                            * \u001b[94moverrides config file\u001b[0m")
			os.Exit(0)
		}
		// -z --completion
		genCompletion(cmd)
		//log for initial checks
		log := mLog.PackageLogger("pre-run")

		if stdin {
			confPath = visorconfig.Stdin
		} else {

			//enforce conditions for pkg and user flags
			if pkg {
				if !root {
					log.Fatal("root permissions required to use the specified config")
				}
				if !pkgconfigexists {
					log.Fatal("config not found")
				}
			}
			if usr {
				if root {
					log.Fatal("cannot use specified config as root")
				}
				if !userconfigexists {
					log.Fatal("config not found")
				}
			}

			//error on multiple configs from flags
			if (pkg && usr) || ((pkg || usr) && (confPath != "")) {
				log.Fatal("Error: multiple configs specified")
			}
			//use package config /opt/skywire/skywire.json
			if pkg {
				confPath = visorconfig.SkywirePath + "/" + visorconfig.ConfigJSON
			}
			//userspace config in $HOME/.skywire/skywire-config.json
			if usr {
				confPath = visorconfig.HomePath() + "/" + visorconfig.ConfigName
			}
			if confPath == "" {
				//default config in current dir ./skywire-config.json
				confPath = visorconfig.ConfigName
			}
			//enforce .json extension
			if !strings.HasSuffix(confPath, ".json") {
				confPath = confPath + ".json"
			}
			//check for the config file
			if _, err := os.Stat(confPath); err != nil {
				//fail here on no config
				log.WithError(err).Fatal("config file not found")
			}
		}
		logBuildInfo(mLog)
		if launchBrowser {
			hypervisorUI = true
		}
		//warn about creating files & directories as root in non root-owned dir
		if visorconfig.OS == "linux" {
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
			if visorconfig.OS == "linux" {
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

func initConfig() *visorconfig.V1 { //nolint
	log := mLog.PackageLogger("visor:config")

	var r io.Reader

	switch confPath {
	case visorconfig.Stdin:
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
		config := visorconfig.GenerateWorkDirConfig(false)
		conf.Hypervisor = &config
	}
	if conf.Hypervisor != nil {
		if *uiAssets == nil {
			log.Fatalf("missing embedded assets for hypervisor ui")
		}
		conf.Hypervisor.UIAssets = *uiAssets
	}
	if noHypervisorUI {
		conf.Hypervisor = nil
	}

	visorconfig.VisorConfigFile = confPath
	return conf
}

const help = "{{if .HasAvailableSubCommands}}{{end}} {{if gt (len .Aliases) 0}}" +
	"{{.NameAndAliases}}{{end}}{{if .HasAvailableSubCommands}}" +
	"Available Commands:{{range .Commands}}{{if (or .IsAvailableCommand)}}" +
	"{{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}" +
	"Flags:\r\n" +
	"{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}\r\n\r\n" +
	"Global Flags:\r\n" +
	"{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}\r\n\r\n"
