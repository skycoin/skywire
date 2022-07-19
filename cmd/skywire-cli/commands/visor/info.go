package clivisor

import (
	"fmt"
	"log"
	"os"
	"unicode/utf8"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var path string
var pkg bool
var web bool

func init() {
	RootCmd.AddCommand(pkCmd)
	pkCmd.Flags().StringVarP(&path, "input", "i", "", "path of input config file.")
	pkCmd.Flags().BoolVarP(&pkg, "pkg", "p", false, "read from /opt/skywire/skywire.json")
	pkCmd.Flags().BoolVarP(&web, "http", "w", false, "format as http response")
	RootCmd.AddCommand(hvpkCmd)
	hvpkCmd.Flags().StringVarP(&path, "input", "i", "", "path of input config file.")
	hvpkCmd.Flags().BoolVarP(&pkg, "pkg", "p", false, "read from /opt/skywire/skywire.json")
	hvpkCmd.Flags().BoolVarP(&web, "http", "w", false, "format as http response")
	RootCmd.AddCommand(summaryCmd)
	RootCmd.AddCommand(buildInfoCmd)
}

var pkCmd = &cobra.Command{
	Use:   "pk",
	Short: "Public key of the visor",
	Run: func(_ *cobra.Command, _ []string) {
		if pkg {
			path = visorconfig.Pkgpath
		}
		if path != "" {
			conf, err := visorconfig.ReadFile(path)
			if err != nil {
				logger.Fatal("Failed to read config:", err)
			}
			fmt.Println(conf.PK.Hex())
		} else {
			client := clirpc.RPCClient()
			overview, err := client.Overview()
			if err != nil {
				logger.Fatal("Failed to connect:", err)
			}
			if web {
				rc := utf8.RuneCountInString(overview.PubKey.String())
				header := fmt.Sprintf("HTTP/1.0 200 OK\r\nContent-Length: %d\r\n", rc)
				fmt.Println(header)
			}
			fmt.Println(overview.PubKey)
		}
	},
}

var hvpkCmd = &cobra.Command{
	Use:   "hvpk",
	Short: "Public key of remote hypervisor",
	Run: func(_ *cobra.Command, _ []string) {
		if pkg {
			path = visorconfig.Pkgpath
		}
		if path != "" {
			conf, err := visorconfig.ReadFile(path)
			if err != nil {
				logger.Fatal("Failed to read config:", err)
			}
			fmt.Println(conf.Hypervisors)
		} else {
			client := clirpc.RPCClient()
			overview, err := client.Overview()
			if err != nil {
				logger.Fatal("Failed to connect:", err)
			}
			fmt.Println(overview.Hypervisors)
		}
	},
}

var summaryCmd = &cobra.Command{
	Use:   "info",
	Short: "Summary of visor info",
	Run: func(_ *cobra.Command, _ []string) {
		summary, err := clirpc.RPCClient().Summary()
		if err != nil {
			log.Fatal("Failed to connect:", err)
		}
		msg := fmt.Sprintf(".:: Visor Summary ::.\nPublic key: %q\nSymmetric NAT: %t\nIP: %s\nDMSG Server: %q\nPing: %q\nVisor Version: %s\nSkybian Version: %s\nUptime Tracker: %s\nTime Online: %f seconds\nBuild Tag: %s\n", summary.Overview.PubKey, summary.Overview.IsSymmetricNAT, summary.Overview.LocalIP, summary.DmsgStats.ServerPK, summary.DmsgStats.RoundTrip, summary.Overview.BuildInfo.Version, summary.SkybianBuildVersion, summary.Health.ServicesHealth, summary.Uptime, summary.BuildTag)
		if _, err := os.Stdout.Write([]byte(msg)); err != nil {
			log.Fatal("Failed to output build info:", err)
		}
	},
}

var buildInfoCmd = &cobra.Command{
	Use:   "version",
	Short: "Version and build info",
	Run: func(_ *cobra.Command, _ []string) {
		client := clirpc.RPCClient()
		overview, err := client.Overview()
		if err != nil {
			log.Fatal("Failed to connect:", err)
		}
		if _, err := overview.BuildInfo.WriteTo(os.Stdout); err != nil {
			log.Fatal("Failed to output build info:", err)
		}
	},
}
