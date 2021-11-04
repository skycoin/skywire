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

	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func init() {
	RootCmd.AddCommand(genConfigCmd)
}

var (
	sk            cipher.SecKey
	output        string
	outputUnset   bool
	replace       bool
	testEnv       bool
	packageConfig bool
	skybianConfig bool
	hypervisor    bool
	hypervisorPKs string
)

func init() {
	genConfigCmd.Flags().Var(&sk, "sk", "if unspecified, a random key pair will be generated.\n")
	genConfigCmd.Flags().StringVarP(&output, "output", "o", "", "path of output config file. default: skywire-config.json")
	genConfigCmd.Flags().BoolVarP(&replace, "replace", "r", false, "rewrite existing config (retains keys).")
	genConfigCmd.Flags().BoolVarP(&packageConfig, "package", "p", false, "use defaults for package-based installations in /opt/skywire")
	genConfigCmd.Flags().BoolVarP(&skybianConfig, "skybian", "s", false, "use defaults paths found in skybian\n writes config to /etc/skywire-config.json")
	genConfigCmd.Flags().BoolVarP(&testEnv, "testenv", "t", false, "use test deployment service.")
	genConfigCmd.Flags().BoolVarP(&hypervisor, "is-hypervisor", "i", false, "generate a hypervisor configuration.")
	genConfigCmd.Flags().StringVar(&hypervisorPKs, "hypervisor-pks", "", "public keys of hypervisors that should be added to this visor")
}

var genConfigCmd = &cobra.Command{
	Use:   "gen",
	Short: "Generates a config file",
	PreRun: func(_ *cobra.Command, _ []string) {
		var err error
		// check to see if output was set
		if output == "" {
			outputUnset = true
			output = "skywire-config.json"
		}
		if output, err = filepath.Abs(output); err != nil {
			logger.WithError(err).Fatal("Invalid output provided.")
		}
	},
	Run: func(_ *cobra.Command, _ []string) {
		mLog := logging.NewMasterLogger()
		mLog.SetLevel(logrus.InfoLevel)

		//Fail on -pst combination
		if (packageConfig && skybianConfig) || (packageConfig && testEnv) || (skybianConfig && testEnv) {
			logger.Fatal("Failed to create config: use of mutually exclusive flags")
		}

		//set output for package and skybian configs if unspecified
		if outputUnset {
			if packageConfig {
				if hypervisor {
					configName := "skywire.json"
					output = filepath.Join(skyenv.PackageSkywirePath(), configName)
				} else { //for visor
					configName := "skywire-visor.json"
					output = filepath.Join(skyenv.PackageSkywirePath(), configName)
				}
			}
			if skybianConfig {
				output = "/etc/skywire-config.json"
			}
		}

		// Read in old config (if any) and obtain old secret key.
		// Otherwise, we generate a new random secret key.
		var sk cipher.SecKey
		if oldConf, ok := readOldConfig(mLog, output, replace); !ok {
			_, sk = cipher.GenerateKeyPair()
		} else {
			sk = oldConf.SK
		}

		// Determine config type to generate.
		var genConf func(log *logging.MasterLogger, confPath string, sk *cipher.SecKey, hypervisor bool) (*visorconfig.V1, error)

		//  default paths for different installations
		if packageConfig {
			genConf = visorconfig.MakePackageConfig
		} else if skybianConfig {
			genConf = visorconfig.MakeDefaultConfig
		} else if testEnv {
			genConf = visorconfig.MakeTestConfig
		} else {
			genConf = visorconfig.MakeDefaultConfig
		}

		// Generate config.
		conf, err := genConf(mLog, output, &sk, hypervisor)
		if err != nil {
			logger.WithError(err).Fatal("Failed to create config.")
		}

		if hypervisorPKs != "" {
			keys := strings.Split(hypervisorPKs, ",")
			for _, key := range keys {
				keyParsed, err := coinCipher.PubKeyFromHex(strings.TrimSpace(key))
				if err != nil {
					logger.WithError(err).Fatalf("Failed to parse hypervisor private key: %s.", key)
				}
				conf.Hypervisors = append(conf.Hypervisors, cipher.PubKey(keyParsed))
			}
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

func readOldConfig(log *logging.MasterLogger, confPath string, replace bool) (*visorconfig.V1, bool) {
	raw, err := ioutil.ReadFile(confPath) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false
		}
		logger.WithError(err).Fatal("Unexpected error occurred when attempting to read old config.")
	}

	if !replace {
		logger.Fatal("Config file already exists. Specify the 'replace, r' flag to replace this.")
	}

	conf, err := visorconfig.Parse(log, confPath, raw)
	if err != nil {
		logger.WithError(err).Fatal("Failed to parse old config file.")
	}

	return conf, true
}
