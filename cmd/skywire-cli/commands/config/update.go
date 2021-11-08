package config

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/cipher"
	coinCipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func init() {
	RootCmd.AddCommand(updateConfigCmd)
}

var (
	addOutput              string
	addInput               string
	environment            string
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
	minHops                int
)

func init() {
	updateConfigCmd.Flags().StringVarP(&addOutput, "output", "o", "skywire-config.json", "path of output config file.")
	updateConfigCmd.Flags().StringVarP(&addInput, "input", "i", "skywire-config.json", "path of input config file.")
	updateConfigCmd.Flags().StringVarP(&environment, "environment", "e", "production", "desired environment (values production or testing)")
	updateConfigCmd.Flags().StringVar(&addHypervisorPKs, "add-hypervisor-pks", "", "public keys of hypervisors that should be added to this visor")
	updateConfigCmd.Flags().BoolVar(&resetHypervisor, "reset-hypervisor-pks", false, "resets hypervisor configuration")

	updateConfigCmd.Flags().StringVar(&setVPNClientKillswitch, "vpn-client-killswitch", "", "change killswitch status of vpn-client")
	updateConfigCmd.Flags().StringVar(&addVPNClientSrv, "add-vpn-client-server", "", "add server address to vpn-client")
	updateConfigCmd.Flags().StringVar(&addVPNClientPasscode, "add-vpn-client-passcode", "", "add passcode of server if needed")
	updateConfigCmd.Flags().BoolVar(&resetVPNclient, "reset-vpn-client", false, "reset vpn-client configurations")

	updateConfigCmd.Flags().StringVar(&addVPNServerPasscode, "add-vpn-server-passcode", "", "add passcode to vpn-server")
	updateConfigCmd.Flags().StringVar(&setVPNServerSecure, "vpn-server-secure", "", "change secure mode status of vpn-server")
	updateConfigCmd.Flags().BoolVar(&resetVPNServer, "reset-vpn-server", false, "reset vpn-server configurations")

	updateConfigCmd.Flags().StringVar(&addSkysocksClientSrv, "add-skysocks-client-server", "", "add skysocks server address to skysock-client")
	updateConfigCmd.Flags().BoolVar(&resetSkysocksClient, "reset-skysocks-client", false, "reset skysocks-client configuration")

	updateConfigCmd.Flags().StringVar(&skysocksPasscode, "add-skysocks-passcode", "", "add passcode to skysocks server")
	updateConfigCmd.Flags().BoolVar(&resetSkysocks, "reset-skysocks", false, "reset skysocks configuration")

	updateConfigCmd.Flags().StringVar(&setPublicAutoconnect, "set-public-autoconnect", "", "change public autoconnect configuration")

	updateConfigCmd.Flags().IntVar(&minHops, "set-minhop", -1, "change min hops value")
}

var updateConfigCmd = &cobra.Command{
	Use:   "update",
	Short: "Updates a config file",
	PreRun: func(_ *cobra.Command, _ []string) {
		var err error
		if output, err = filepath.Abs(addOutput); err != nil {
			logger.WithError(err).Fatal("Invalid config output.")
		}
	},
	Run: func(_ *cobra.Command, _ []string) {
		mLog := logging.NewMasterLogger()
		mLog.SetLevel(logrus.InfoLevel)
		f, err := os.Open(addInput) // nolint: gosec
		if err != nil {
			mLog.WithError(err).
				WithField("filepath", addInput).
				Fatal("Failed to read config file.")
		}

		raw, err := ioutil.ReadAll(f)
		if err != nil {
			mLog.WithError(err).Fatal("Failed to read config.")
		}

		conf, ok := visorconfig.Parse(mLog, addInput, raw)
		if ok != nil {
			mLog.WithError(err).Fatal("Failed to parse config.")
		}

		if addHypervisorPKs != "" {
			keys := strings.Split(addHypervisorPKs, ",")
			for _, key := range keys {
				keyParsed, err := coinCipher.PubKeyFromHex(strings.TrimSpace(key))
				if err != nil {
					logger.WithError(err).Fatalf("Failed to parse hypervisor private key: %s.", key)
				}
				conf.Hypervisors = append(conf.Hypervisors, cipher.PubKey(keyParsed))
			}
		}

		switch environment {
		case "production":
			visorconfig.SetDefaultProductionValues(conf)
		case "testing":
			visorconfig.SetDefaultTestingValues(conf)
		default:
			logger.Fatal("Unrecognized environment value: ", environment)
		}

		if resetHypervisor {
			conf.Hypervisors = []cipher.PubKey{}
		}

		switch setVPNClientKillswitch {
		case "true":
			changeAppsConfig(conf, "vpn-client", "--killswitch", setVPNClientKillswitch)
		case "false":
			changeAppsConfig(conf, "vpn-client", "--killswitch", setVPNClientKillswitch)
		default:
			logger.Fatal("Unrecognized environment value: ", setVPNClientKillswitch)
		}

		if addVPNClientSrv != "" {
			keyParsed, err := coinCipher.PubKeyFromHex(strings.TrimSpace(addVPNClientSrv))
			if err != nil {
				logger.WithError(err).Fatalf("Failed to parse hypervisor private key: %s.", addVPNClientSrv)
			}
			changeAppsConfig(conf, "vpn-client", "--srv", keyParsed.Hex())
		}

		if addVPNClientPasscode != "" {
			changeAppsConfig(conf, "vpn-client", "--passcode", addVPNClientPasscode)
		}

		if resetVPNclient {
			resetAppsConfig(conf, "vpn-client")
		}

		if addVPNServerPasscode != "" {
			changeAppsConfig(conf, "vpn-server", "--passcode", addVPNServerPasscode)
		}

		switch setVPNServerSecure {
		case "true":
			changeAppsConfig(conf, "vpn-server", "--secure", setVPNServerSecure)
		case "false":
			changeAppsConfig(conf, "vpn-server", "--secure", setVPNServerSecure)
		default:
			logger.Fatal("Unrecognized environment value: ", setVPNServerSecure)
		}

		if resetVPNServer {
			resetAppsConfig(conf, "vpn-server")
		}

		if addSkysocksClientSrv != "" {
			keyParsed, err := coinCipher.PubKeyFromHex(strings.TrimSpace(addSkysocksClientSrv))
			if err != nil {
				logger.WithError(err).Fatalf("Failed to parse hypervisor private key: %s.", addSkysocksClientSrv)
			}
			changeAppsConfig(conf, "skysocks-client", "--srv", keyParsed.Hex())
		}

		if resetSkysocksClient {
			resetAppsConfig(conf, "skysocks-client")
		}

		if skysocksPasscode != "" {
			changeAppsConfig(conf, "skysocks", "--passcode", skysocksPasscode)
		}

		if resetSkysocks {
			resetAppsConfig(conf, "skysocks")
		}

		switch setPublicAutoconnect {
		case "true":
			conf.Transport.PublicAutoconnect = true
		case "false":
			conf.Transport.PublicAutoconnect = false
		default:
			logger.Fatal("Unrecognized environment value: ", setPublicAutoconnect)
		}

		if minHops >= 0 {
			conf.Routing.MinHops = uint16(minHops)
		}

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
