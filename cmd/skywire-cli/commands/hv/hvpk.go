package clihv

import (
	"fmt"

	"github.com/bitfield/script"
	"github.com/spf13/cobra"
)

func init() {
	ipaddr, err := script.Exec(`bash -c "_gateway=$(ip route show | grep -i 'default via'| awk '{print $3 }'); _ip=${_gateway%.*}.2; printf ${_ip}"`).String()
	if err != nil {
		err.Error()
	}

	RootCmd.AddCommand(pkCmd)
	pkCmd.Flags().IntVarP(&port, "port", "p", 7998, "port to query")
	pkCmd.Flags().StringVarP(&ipadd, "ip", "i", ipaddr, "ip address to query")
}

var pkCmd = &cobra.Command{
	Use:   "pk",
	Short: "Fetch Hypervisor Public Key",
	Run: func(_ *cobra.Command, _ []string) {
		hvpk, err := script.Exec(`bash -c "curl ` + fmt.Sprintf("%s:%d", ipadd, port) + `"`).String()
		if err != nil {
			err.Error()
		}
		fmt.Printf("%s", hvpk)
	},
}
