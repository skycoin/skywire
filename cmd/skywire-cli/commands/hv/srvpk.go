package hv

import (
	"github.com/bitfield/script"
	"github.com/spf13/cobra"
)

var sourcerun bool
var skywirecli string
var port string

func init() {
	RootCmd.AddCommand(srvpkCmd)
	srvpkCmd.Flags().BoolVarP(&sourcerun, "src", "s", false, "'go run' using the skywire sources")
	srvpkCmd.Flags().StringVarP(&port, "port", "p", "7998", "port to serve")
}

// RootCmd contains commands that interact with the skywire-visor
var srvpkCmd = &cobra.Command{
	Use:   "srvpk",
	Short: "http endpoint for `skywire-cli visor pk`",
	Run: func(_ *cobra.Command, _ []string) {
		if !sourcerun {
			skywirecli = "skywire-cli"
		} else {
			skywirecli = "go run cmd/skywire-cli/skywire-cli.go"
		}
		for {
			_, err := script.Exec(`nc -l -p ` + port + ` -e '` + skywirecli + ` visor pk -w'`).Stdout()
			if err != nil {
				err.Error()
				//os.Exit(1)
			}
		}
	},
}
