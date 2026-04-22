// Package clivisor cmd/skywire-cli/commands/visor/info.go
package clivisor

import (
	"encoding/json"
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/network"
)

var geoipURL string

func init() {
	RootCmd.Flags().StringVar(&geoipURL, "geoip", deployment.Prod.GeoIP, "url of geoip service")
	RootCmd.AddCommand(ipCmd)
}

var ipCmd = &cobra.Command{
	Use:   "ip",
	Short: "IP information of network",
	Long:  "\n  IP information of network",
	Run: func(cmd *cobra.Command, _ []string) {
		mLog := logging.NewMasterLogger()
		mLog.SetLevel(logrus.PanicLevel)
		logger := mLog.PackageLogger("visor_ip_information")

		ip, err := getIPAddress(cmd.Flags(), geoipURL)
		if err != nil {
			internal.Catch(cmd.Flags(), err)
		}
		isPublic := isPublic(cmd.Flags(), logger)
		internal.PrintOutput(cmd.Flags(), ip, fmt.Sprintf("IP: %s\nPublic Status: %s\n", ip, isPublic))
	},
}

func getIPAddress(cmdFlags *pflag.FlagSet, geoipURL string) (string, error) {
	var info ipInfo

	body, err := clirpc.FetchServiceURL(cmdFlags, geoipURL)
	if err != nil {
		return info.IP, err
	}
	err = json.Unmarshal(body, &info)
	if err != nil {
		return info.IP, err
	}
	return info.IP, err
}

type ipInfo struct {
	IP string `json:"ip_address"`
}

func isPublic(cmdFlags *pflag.FlagSet, logger *logging.Logger) string {
	stunServers, err := getStunServers(cmdFlags)
	if err != nil {
		return err.Error()
	}
	sc := network.GetStunDetails(stunServers, logger)
	return sc.NATType.String()
}

func getStunServers(cmdFlags *pflag.FlagSet) ([]string, error) {
	var info stunInfo

	confURL := deployment.ProdConf.Conf
	body, err := clirpc.FetchServiceURL(cmdFlags, confURL+"/")
	if err != nil {
		return info.Stun, err
	}
	err = json.Unmarshal(body, &info)
	if err != nil {
		return info.Stun, err
	}
	return info.Stun, err
}

type stunInfo struct {
	Stun []string `json:"stun_servers"`
}
