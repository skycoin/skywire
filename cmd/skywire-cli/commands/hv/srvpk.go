package hv

import (
	"log"
	"github.com/spf13/cobra"
	"github.com/bitfield/script"
)

func init() {
	RootCmd.AddCommand(srvpkCmd)
}

// RootCmd contains commands that interact with the skywire-visor
var srvpkCmd = &cobra.Command{
	Use:   "srvpk",
	Short: "serve hypervisor public key",
	Run: func(_ *cobra.Command, _ []string) {
		//TODO: get the actual port from config instead of using default value here
		_, err := script.Exec(`while true; do { echo -ne "HTTP/1.0 200 OK\r\nContent-Length: 1\r\n\r\n"; skywire-cli visor pk ; } | nc -l -p 7998 ; done`).Stdout()
		if err != nil {
			log.Printf("error occured")
			os.Exit(1)
		}
	},
}
