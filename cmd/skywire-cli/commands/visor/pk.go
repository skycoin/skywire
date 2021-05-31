package visor

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	RootCmd.AddCommand(pkCmd)
}

var pkCmd = &cobra.Command{
	Use:   "pk",
	Short: "Obtains the public key of the visor",
	Run: func(_ *cobra.Command, _ []string) {

		client := rpcClient()
		overview, err := client.Overview()
		if err != nil {
			logger.Fatal("Failed to connect:", err)
		}

		fmt.Println(overview.PubKey)
	},
}
