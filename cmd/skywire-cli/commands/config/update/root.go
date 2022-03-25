package update

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var (
	addOutput              string
	addInput               string
	environment            string
	updateEndpoints        bool
	addHypervisorPKs       string
	resetHypervisor        bool
	setVPNClientKillswitch string
	addVPNClientSrv        string
	addVPNClientPasscode   string
	resetVPNclient         bool
	addVPNServerPasscode   string
	setVPNServerSecure     string
	resetVPNServer         bool
	addSkysocksClientSrv   string
	resetSkysocksClient    bool
	skysocksPasscode       string
	resetSkysocks          bool
	setPublicAutoconnect   string
	serviceConfURL         string
	minHops                int
	output                 string
)

var logger = logging.MustGetLogger("skywire-cli")

func init() {
	RootCmd.Flags().SortFlags = false
	RootCmd.Flags().BoolVarP(&updateEndpoints, "endpoints", "a", false, "update server endpoints")
	RootCmd.Flags().StringVarP(&serviceConfURL, "url", "b", "skywire.skycoin.com", "service configuration URL")
	RootCmd.Flags().StringVarP(&environment, "environment", "c", "production", "desired environment (values production or testing)")
	RootCmd.Flags().StringVar(&setPublicAutoconnect, "public-autoconn", "", "change public autoconnect configuration")
	RootCmd.Flags().IntVar(&minHops, "set-minhop", -1, "change min hops value")
	RootCmd.PersistentFlags().StringVarP(&addInput, "input", "i", "skywire-config.json", "path of input config file.")
	RootCmd.PersistentFlags().StringVarP(&addOutput, "output", "o", "skywire-config.json", "path of output config file.")
	RootCmd.PersistentFlags().BoolVarP(&pkg, "pkg", "p", false, "read from /opt/skywire/skywire.json")
}

// RootCmd contains commands that update the config
var RootCmd = &cobra.Command{
	Use:   "update",
	Short: "update a config file",
	PreRun: func(_ *cobra.Command, _ []string) {
		var err error
		if output, err = filepath.Abs(addOutput); err != nil {
			logger.WithError(err).Fatal("Invalid config output.")
		}
	},
	Run: func(cmd *cobra.Command, _ []string) {
		mLog := logging.NewMasterLogger()
		mLog.SetLevel(logrus.InfoLevel)
		if cmd.Flags().Changed("serviceConfURL") {
			updateEndpoints = true
		}
		var services visorconfig.Services
		if updateEndpoints {
			urlstr := []string{"http://", serviceConfURL, "/config"}
			serviceConfURL = strings.Join(urlstr, "")
			client := http.Client{
				Timeout: time.Second * 2, // Timeout after 2 seconds
			}
			req, err := http.NewRequest(http.MethodGet, serviceConfURL, nil)
			if err != nil {
				mLog.Fatal(err)
			}
			res, err := client.Do(req)
			if err != nil {
				mLog.Fatal(err)
			}
			if res.Body != nil {
				defer res.Body.Close() //nolint
			}
			body, err := ioutil.ReadAll(res.Body)
			if err != nil {
				mLog.Fatal(err)
			}
			err = json.Unmarshal(body, &services)
			if err != nil {
				mLog.Fatal(err)
			}
		}

		conf, ok := visorconfig.ReadConfig(addInput)
		if ok != nil {
			mLog.WithError(ok).Fatal("Failed to parse config.")
		}

		/*
			switch environment {
			case "production":
				visorconfig.SetDefaultProductionValues(conf)
			case "testing":
				visorconfig.SetDefaultTestingValues(conf)
			default:
				logger.Fatal("Unrecognized environment value: ", environment)
			}
		*/
		switch setPublicAutoconnect {
		case "true":
			conf.Transport.PublicAutoconnect = true
		case "false":
			conf.Transport.PublicAutoconnect = false
		case "":
			break
		default:
			logger.Fatal("Unrecognized public autoconnect value: ", setPublicAutoconnect)
		}

		if minHops >= 0 {
			conf.Routing.MinHops = uint16(minHops)
		}

		saveConfig(conf)
	},
}

func changeAppsConfig(conf *visorconfig.V1, appName string, argName string, argValue string) {
	apps := conf.Launcher.Apps
	for index := range apps {
		if apps[index].Name != appName {
			continue
		}
		updated := false
		for ind, arg := range apps[index].Args {
			if arg == argName {
				apps[index].Args[ind+1] = argValue
				updated = true
			}
		}
		if !updated {
			apps[index].Args = append(apps[index].Args, argName, argValue)
		}
	}
}

func resetAppsConfig(conf *visorconfig.V1, appName string) {
	apps := conf.Launcher.Apps
	for index := range apps {
		if apps[index].Name == appName {
			apps[index].Args = []string{}
		}
	}
}

func saveConfig(conf *visorconfig.V1) {
	// Save config to file.
	if err := conf.Flush(); err != nil {
		logger.WithError(err).Fatal("Failed to flush config to file.")
	}

	// Print results.
	j, err := json.MarshalIndent(conf, "", "\t")
	if err != nil {
		logger.WithError(err).Fatal("An unexpected error occurred. Please contact a developer.")
	}
	logger.Infof("Updated file '%s' to: %s", output, j)
}
