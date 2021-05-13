package visor

import (
	"log"
	"os"

	"github.com/spf13/cobra"
)

func init() {
	RootCmd.AddCommand(buildInfoCmd)
}

var buildInfoCmd = &cobra.Command{
	Use:   "version",
	Short: "Obtains version and build info of the node",
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
