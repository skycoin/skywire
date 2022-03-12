package visor

import (
	"fmt"
	"log"
	"io/ioutil"
	"os"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var addInput string

func init() {
	RootCmd.AddCommand(pkCmd)
	pkCmd.Flags().StringVarP(&addInput, "input", "i", "", "path of input config file.")

	RootCmd.AddCommand(hvpkCmd)
	hvpkCmd.Flags().StringVarP(&addInput, "input", "i", "", "path of input config file.")

	RootCmd.AddCommand(
		summaryCmd,
		buildInfoCmd,
	)
}

var pkCmd = &cobra.Command{
	Use:   "pk",
	Short: "public key of the visor",
	Run: func(_ *cobra.Command, _ []string) {
		if addInput != "" {
			conf := readConfig(addInput)
			fmt.Println(conf.PK.Hex())
			} else {
				client := rpcClient()
				overview, err := client.Overview()
				if err != nil {
					logger.Fatal("Failed to connect:", err)
				}
				fmt.Println(overview.PubKey)
			}
		},
	}

	var hvpkCmd = &cobra.Command{
		Use:   "hv",
		Short: "show hypervisor(s)",
		Run: func(_ *cobra.Command, _ []string) {
			if addInput != "" {
				conf := readConfig(addInput)
				fmt.Println(conf.Hypervisors)
				} else {
					client := rpcClient()
					overview, err := client.Overview()
					if err != nil {
						logger.Fatal("Failed to connect:", err)
					}
					fmt.Println(overview.Hypervisors)
				}
			},
		}

		func readConfig(path string) *visorconfig.V1 {
			mLog := logging.NewMasterLogger()
			mLog.SetLevel(logrus.InfoLevel)

			f, err := os.Open(path) // nolint: gosec
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
			return conf
		}


var summaryCmd = &cobra.Command{
	Use:   "info",
	Short: "summary of visor info",
	Run: func(_ *cobra.Command, _ []string) {
		summary, err := rpcClient().Summary()
		if err != nil {
			log.Fatal("Failed to connect:", err)
		}
		msg := fmt.Sprintf(".:: Visor Summary ::.\nPublic key: %q\nSymmetric NAT: %t\nIP: %s\nDMSG Server: %q\nPing: %q\nVisor Version: %s\nSkybian Version: %s\nUptime Tracker: %s\nTime Online: %f seconds\nBuild Tag: %s\n", summary.Overview.PubKey, summary.Overview.IsSymmetricNAT, summary.Overview.LocalIP, summary.DmsgStats.ServerPK, summary.DmsgStats.RoundTrip, summary.Overview.BuildInfo.Version, summary.SkybianBuildVersion, summary.Health.ServicesHealth, summary.Uptime, summary.BuildTag)
		if _, err := os.Stdout.Write([]byte(msg)); err != nil {
			log.Fatal("Failed to output build info:", err)
		}
	},
}

var buildInfoCmd = &cobra.Command{
	Use:   "version",
	Short: "version and build info",
	Run: func(_ *cobra.Command, _ []string) {
		client := rpcClient()
		overview, err := client.Overview()
		if err != nil {
			log.Fatal("Failed to connect:", err)
		}

		if _, err := overview.BuildInfo.WriteTo(os.Stdout); err != nil {
			log.Fatal("Failed to output build info:", err)
		}
	},
}
