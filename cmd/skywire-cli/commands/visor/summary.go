package visor

import (
	"fmt"
	"log"
	"os"

	"github.com/spf13/cobra"
)

func init() {
	RootCmd.AddCommand(summaryCmd)
}

var summaryCmd = &cobra.Command{
	Use:   "summary",
	Short: "Obtains summary of visor information",
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
