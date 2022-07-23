package clihv

import (
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/netutil"
)

func init() {
	if os.Getenv("SKYBIAN") == "true" {
		RootCmd.AddCommand(pkCmd)
	}
	localIPs, err = netutil.DefaultNetworkInterfaceIPs()
	if err != nil {
		logger.WithError(err).Fatalln("Could not determine network interface IP address")
	}
	if len(localIPs) == 0 {
		localIPs = append(localIPs, net.ParseIP("192.168.0.1"))
	}

	pkCmd.Flags().StringVarP(&ipAddr, "ip", "i", trimStringFromDot(localIPs[0].String())+".2:7998", "ip:port to query")
}

func trimStringFromDot(s string) string {
	if idx := strings.LastIndex(s, "."); idx != -1 {
		return s[:idx]
	}
	return s
}

var pkCmd = &cobra.Command{
	Use:   "pk",
	Short: "Fetch Hypervisor Public Key",
	Run: func(_ *cobra.Command, _ []string) {
		req, err := http.NewRequest(http.MethodGet, "http://"+ipAddr, nil)
		if err != nil {
			logger.WithError(err).Fatalln("failed to create http request")
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			logger.WithError(err).Fatalln("failed to execte http request")
		}
		resBody, err := ioutil.ReadAll(res.Body)
		if err != nil {
			logger.WithError(err).Fatalln("failed to read http response")
		}
		fmt.Printf("%s", resBody)
	},
}
