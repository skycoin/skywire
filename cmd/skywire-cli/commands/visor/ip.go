// Package clivisor cmd/skywire-cli/commands/visor/info.go
package clivisor

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/transport/network"
)

func init() {
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

		ip, err := getIPAddress()
		if err != nil {
			internal.Catch(cmd.Flags(), err)
		}
		isPublic := isPublic(logger)
		internal.PrintOutput(cmd.Flags(), ip, fmt.Sprintf("IP: %s\nPublic Status: %s\n", ip, isPublic))
	},
}

func getIPAddress() (string, error) {
	var info ipInfo
	var resp *http.Response
	var err error

	resp, err = http.Get("https://ip.skycoin.com/")
	if err != nil {
		resp, err = http.Get("https://ip.plaintext.ir/")
		if err != nil {
			return info.IP, err
		}
	}
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return info.IP, err
	}
	err = json.Unmarshal(respBody, &info)
	if err != nil {
		return info.IP, err
	}
	return info.IP, err
}

type ipInfo struct {
	IP string `json:"ip_address"`
}

func isPublic(logger *logging.Logger) string {
	stunServers, err := getStunServers()
	if err != nil {
		return err.Error()
	}
	sc := network.GetStunDetails(stunServers, logger)
	return sc.NATType.String()
}

func getStunServers() ([]string, error) {
	var info stunInfo

	resp, err := http.Get("https://conf.skywire.skycoin.com/")
	if err != nil {
		return info.Stun, err
	}
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return info.Stun, err
	}
	err = json.Unmarshal(respBody, &info)
	if err != nil {
		return info.Stun, err
	}
	return info.Stun, err
}

type stunInfo struct {
	Stun []string `json:"stun_servers"`
}
