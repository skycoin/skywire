// Package flags internal/flags/flags.go
package flags

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/skycoin/dmsg/pkg/dmsg"
)

var (
	// DmsgDiscURL is the dmsg discovery URL
	DmsgDiscURL = dmsg.DiscURL(false)

	// DmsgDiscAddr is the dmsg discovery dmsg address
	DmsgDiscAddr = dmsg.DiscAddr(false)

	// DmsgSessions is the default number of sessions i.e. servers to connect to
	DmsgSessions = 2

	// DmsgHTTPPath is the path to the dmsghttp-config.json which overrides embedded defaults
	DmsgHTTPPath string

	// UseHTTP connect to the dmsg discoverey over plain http or dmsghttp
	UseHTTP = false

	// UseDC use dmsg direct client with embedded dmsg server configuration and don't connect to discovery server
	UseDC = false
)

// InitFlags is used to set command flags for the above variables
func InitFlags(cmd *cobra.Command) {
	cmd.Flags().BoolVarP(&UseHTTP, "http", "Z", UseHTTP, "use regular http to connect to DMSG Discovery")
	cmd.Flags().BoolVarP(&UseDC, "direct", "B", UseDC, "use dmsg-direct client & don't connect to DMSG Discovery")
	cmd.Flags().StringVarP(&DmsgDiscURL, "disc-url", "U", DmsgDiscURL, "DMSG Discovery URL\033[0m\n\r")
	cmd.Flags().StringVarP(&DmsgDiscAddr, "disc-addr", "A", DmsgDiscAddr, "DMSG Discovery dmsg address\033[0m\n\r")
	cmd.Flags().StringVarP(&DmsgHTTPPath, "dmsgconf", "D", "", "dmsghttp-config path")
	cmd.Flags().IntVarP(&DmsgSessions, "sess", "e", DmsgSessions, "number of DMSG Servers to connect to\033[0m\n\r")
}

// InitConfig is used to set command flags for the above variables
func InitConfig() error {
	var err error
	if DmsgHTTPPath != "" {
		dmsg.DmsghttpJSON, err = os.ReadFile(DmsgHTTPPath) //nolint
		if err != nil {
			return err
		}
		err = dmsg.InitConfig()
		if err != nil {
			return err
		}
	}
	return err

}
