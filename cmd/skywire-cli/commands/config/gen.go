package config

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	coinCipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/util/logging"
	//ccobra "github.com/ivanpirog/coloredcobra"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var (
	sk                 cipher.SecKey
	output             string
	stdout             bool
	regen            bool
	replaceHypervisors bool
	testEnv            bool
	pkgEnv             bool
	hypervisor         bool
	hypervisorPKs      string
	dmsgHTTP           bool
	publicRPC          bool
	vpnServerEnable    bool
	disableAUTH        bool
	enableAUTH         bool
	selectedOS         string
	disableApps        string
	bestProtocol       bool
	serviceConfURL     string
	services visorconfig.Services
	force              bool
	print		string
	hide	bool
	outunset bool
	svcconf            = strings.ReplaceAll(serviceconfaddr, "http://", "") //skyenv.DefaultServiceConfAddr
)

const serviceconfaddr = "http://skywire.skycoin.com"

func init() {
	genConfigCmd.Flags().SortFlags = false
	RootCmd.AddCommand(genConfigCmd)
	genConfigCmd.Flags().StringVarP(&serviceConfURL, "url", "a", svcconf, "service conf URL")
	genConfigCmd.Flags().BoolVarP(&bestProtocol, "bestproto", "b", false, "best protocol (dmsg | direct) based on location")
	genConfigCmd.Flags().BoolVarP(&disableAUTH, "noauth", "c", false, "disable authentication for hypervisor UI")
	genConfigCmd.Flags().BoolVarP(&dmsgHTTP, "dmsghttp", "d", false, "use dmsg connection to skywire services")
	genConfigCmd.Flags().BoolVarP(&enableAUTH, "auth", "e", false, "enable auth on hypervisor UI")
	genConfigCmd.Flags().BoolVarP(&force, "force", "f", false, "remove pre-existing config")
	genConfigCmd.Flags().StringVarP(&disableApps, "disableapps", "g", "", "comma separated list of apps to disable")
	genConfigCmd.Flags().BoolVarP(&hypervisor, "ishv", "i", false, "hypervisor configuration")
	genConfigCmd.Flags().StringVarP(&hypervisorPKs, "hvpks", "j", "", "list of public keys to use as hypervisor")
	genConfigCmd.Flags().StringVarP(&selectedOS, "os", "k", "linux", "(linux / macos / windows) paths")
	genConfigCmd.Flags().BoolVarP(&stdout, "stdout", "n", false, "write config to stdout")
	genConfigCmd.Flags().StringVarP(&output, "out", "o", "", "output config default: skywire-config.json")
	genConfigCmd.Flags().BoolVarP(&pkgEnv, "package", "p", false, "use paths for package (/opt/skywire)")
	genConfigCmd.Flags().BoolVarP(&publicRPC, "publicrpc", "q", false, "allow rpc requests from LAN")
	genConfigCmd.Flags().BoolVarP(&regen, "regen", "r", false, "re-generate existing config & retain keys")
	genConfigCmd.Flags().VarP(&sk, "sk", "s", "if unspecified, a random key pair will be generated\n\r")
	genConfigCmd.Flags().BoolVarP(&testEnv, "testenv", "t", false, "use test deployment service")
	genConfigCmd.Flags().BoolVarP(&vpnServerEnable, "servevpn", "v", false, "enable vpn server")
	genConfigCmd.Flags().BoolVarP(&hide, "hide", "w",  false, "dont print the config to the terminal")
	genConfigCmd.Flags().BoolVarP(&replaceHypervisors, "retainhv", "x", false, "retain existing hypervisors with replace")
	genConfigCmd.Flags().StringVar(&print, "print", "", "parse test ; read config from file & print")
	genConfigCmd.Flags().MarkHidden("print")
}

var genConfigCmd = &cobra.Command{
	Use:   "gen",
	Short: "generate a config file",
//	Long: `
//#Hypervisor on linux:
//skywire-cli config gen -bipr --enable-auth`,
	PreRun: func(_ *cobra.Command, _ []string) {
		//
		if output == "" {
			output = "skywire-config.json"
			outunset = true
		}
		if (force) && (regen) {
			logger.Fatal("Use of mutually exclusive flags: -f --force cannot override -r --regen")
		}
		var err error
		if output == visorconfig.StdoutName {
			stdout = true
		}
		if (stdout) && (hide) {
			logger.Fatal("Use of mutually exclusive flags: -w --hide and -n --stdout")
		}
		if (print == "") && !stdout {
			if output, err = filepath.Abs(output); err != nil {
				logger.WithError(err).Fatal("Invalid output provided.")
			}
			if !regen {
				//check if the config exists
				if _, err:= os.Stat(output); err == nil {
					//error config exists !regen
					logger.Fatal("Config file already exists. Specify the '-r --regen' flag to regenerate.")
				}
			}
		}
	},
	Run: func(cmd *cobra.Command, _ []string) {
		mLog := logging.NewMasterLogger()
		mLog.SetLevel(logrus.InfoLevel)

		if print != "" {
			conf, err := visorconfig.ReadConfig(print)
			if err != nil {
				mLog.Fatal("Failed:", err)
			}
			j, err := json.MarshalIndent(conf, "", "\t")
			if err != nil {
				mLog.WithError(err).Fatal("An unexpected error occurred. Please contact a developer.")
			}
			if !stdout {
				mLog.Infof("Updated file '%s' to: %s", output, j)
			} else {
				fmt.Printf("%s", j)
			}
			os.Exit(0)
		}

		if force {
			err := os.Remove(output)
			if err != nil {
				mLog.WithError(err).Fatal("Could not remove file")
			}
		}
		//fetch service URLs from endpoint
		urlstr := []string{"http://", serviceConfURL, "/config"}
		serviceConf := strings.Join(urlstr, "")
		client := http.Client{
			Timeout: time.Second * 2, // Timeout after 2 seconds
		}
		//create the http request
		req, err := http.NewRequest(http.MethodGet, serviceConf, nil)
		if err != nil {
			mLog.Fatal(err)
		}
		//check for errors in the response
		res, err := client.Do(req)
		if err != nil {
			if serviceConfURL != svcconf {
				//if serviceConfURL was changed this error should be fatal
				mLog.WithError(err).Fatal("Failed to fetch servers\n")
			} else { //otherwise just error and continue
				//silence errors for stdout
				if !stdout {
					mLog.WithError(err).Error("Failed to fetch servers\n")
					mLog.Warn("Falling back on hardcoded servers")
				}
			}
		} else {
			// nil error from client.Do(req)
			if res.Body != nil {
				defer res.Body.Close() //nolint
			}
			body, err := ioutil.ReadAll(res.Body)
			if err != nil {
				mLog.Fatal(err)
			}
			//fill in services struct with the response
			err = json.Unmarshal(body, &services)
			if err != nil {
				mLog.Fatal(err)
			}
		}

		if !stdout {
			//set output for package and skybian configs
			if outunset {
			if pkgEnv {
				configName := "skywire-visor.json"
				if hypervisor {
					configName = "skywire.json"
				}
					output = filepath.Join(skyenv.PackageSkywirePath(), configName)
				}
			}
		}

		// Read in old config and obtain old secret key or generate a new random secret key
		var sk cipher.SecKey
		if !stdout {
			if oldConf, err := visorconfig.ReadConfig(output); err != nil {
				_, sk = cipher.GenerateKeyPair()
			} else {
				sk = oldConf.SK
			}
		}

		conf, err := visorconfig.MakeDefaultConfig(mLog, output, &sk, pkgEnv, testEnv, dmsgHTTP, hypervisor, services)
		if err != nil {
			logger.WithError(err).Fatal("Failed to create config.")
		}

		// Manipulate Hypervisor PKs
		if hypervisorPKs != "" {
			keys := strings.Split(hypervisorPKs, ",")
			for _, key := range keys {
				keyParsed, err := coinCipher.PubKeyFromHex(strings.TrimSpace(key))
				if err != nil {
					logger.WithError(err).Fatalf("Failed to parse hypervisor private key: %s.", key)
				}
				conf.Hypervisors = append(conf.Hypervisors, cipher.PubKey(keyParsed))

				// Compare key value and visor PK, if same, then this visor should be hypervisor
				if key == conf.PK.Hex() {
					hypervisor = true
					conf, err = visorconfig.MakeDefaultConfig(mLog, output, &sk, pkgEnv, testEnv, dmsgHTTP, hypervisor, services)
					if err != nil {
						logger.WithError(err).Fatal("Failed to create config.")
					}
					conf.Hypervisors = []cipher.PubKey{}
					break
				}
			}
		}

		if bestProtocol {
			if netutil.LocalProtocol() {
				dmsgHTTP = true
			}
		}
		// Read in old config (if any) and obtain old hypervisors.
		if replaceHypervisors {
			if oldConf, err := visorconfig.ReadConfig(output); err != nil {
				conf.Hypervisors = oldConf.Hypervisors
			}
		}

		// Change rpc address from local to public
		if publicRPC {
			conf.CLIAddr = ":3435"
		}

		// Set autostart enable for vpn-server
		if vpnServerEnable {
			for i, app := range conf.Launcher.Apps {
				if app.Name == "vpn-server" {
					conf.Launcher.Apps[i].AutoStart = true
				}
			}
		}

		// Disable apps that listed on --disable-apps flag
		if disableApps != "" {
			apps := strings.Split(disableApps, ",")
			appsSlice := make(map[string]bool)
			for _, app := range apps {
				appsSlice[app] = true
			}
			var newConfLauncherApps []launcher.AppConfig
			for _, app := range conf.Launcher.Apps {
				if _, ok := appsSlice[app.Name]; !ok {
					newConfLauncherApps = append(newConfLauncherApps, app)
				}
			}
			conf.Launcher.Apps = newConfLauncherApps
		}

		// Make false EnableAuth for hypervisor UI by --disable-auth flag
		if disableAUTH {
			if hypervisor {
				conf.Hypervisor.EnableAuth = false
			}
		}

		// Set EnableAuth true  for hypervisor UI by --enable-auth flag
		if enableAUTH {
			if hypervisor {
				conf.Hypervisor.EnableAuth = true
			}
		}

		// Check OS and enable auth for windows or macos
		if selectedOS == "windows" || selectedOS == "macos" {
			if hypervisor {
				conf.Hypervisor.EnableAuth = true
			}
		}

		if !stdout {
			// Save config to file.
			if err := conf.Flush(); err != nil {
				logger.WithError(err).Fatal("Failed to flush config to file.")
			}
		}
		// Print results.
		j, err := json.MarshalIndent(conf, "", "\t")
		if err != nil {
			logger.WithError(err).Fatal("An unexpected error occurred. Please contact a developer.")
		}
		if !stdout {
			if hide {
				logger.Infof("Updated file '%s'", output)
				} else {
				logger.Infof("Updated file '%s' to: %s", output, j)
			}
		} else {
			fmt.Printf("%s", j)
		}

	},
}
