// Package clivisor cmd/skywire-cli/commands/visor/info.go
package clivisor

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var path string
var pkg bool

func init() {
	RootCmd.AddCommand(pkCmd)
	pkCmd.Flags().StringVarP(&path, "input", "i", "", "path of input config file.")
	pkCmd.Flags().BoolVarP(&pkg, "pkg", "p", false, "read from "+fmt.Sprintf("%v", visorconfig.PackageConfig()))
	RootCmd.AddCommand(summaryCmd)
	RootCmd.AddCommand(buildInfoCmd)
	RootCmd.AddCommand(portsCmd)
}

var pkCmd = &cobra.Command{
	Use:   "pk",
	Short: "Public key of the visor",
	Long:  "\n  Public key of the visor",
	Run: func(cmd *cobra.Command, _ []string) {
		if pkg {
			path = visorconfig.SkywireConfig()
		}
		var outputPK string
		if path != "" {
			conf, err := visorconfig.ReadFile(path)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to read config: %v", err))
			}
			outputPK = conf.PK.Hex()
		} else {
			rpcClient, err := clirpc.Client(cmd.Flags())
			if err != nil {
				os.Exit(1)
			}
			overview, err := rpcClient.Overview()
			if err != nil {
				internal.PrintFatalRPCError(cmd.Flags(), err)
			}
			outputPK = overview.PubKey.Hex() + "\n"
		}
		internal.PrintOutput(cmd.Flags(), strings.TrimSuffix(outputPK, "\n"), outputPK)
	},
}

var summaryCmd = &cobra.Command{
	Use:   "info",
	Short: "Summary of visor info",
	Long:  "\n  Summary of visor info",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		summary, err := rpcClient.Summary()
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}

		// Build list of connected DMSG servers
		dmsgServersStr := ""
		for i, server := range summary.ConnectedDmsgServers {
			if i > 0 {
				dmsgServersStr += "\n              "
			}
			dmsgServersStr += server
		}
		if dmsgServersStr == "" {
			dmsgServersStr = "(none)"
		}

		// Build geo location string
		geoStr := ""
		if summary.Overview.CityName != "" || summary.Overview.RegionName != "" || summary.Overview.CountryCode != "" {
			parts := []string{}
			if summary.Overview.CityName != "" {
				parts = append(parts, summary.Overview.CityName)
			}
			if summary.Overview.RegionName != "" {
				parts = append(parts, summary.Overview.RegionName)
			}
			if summary.Overview.CountryCode != "" {
				parts = append(parts, summary.Overview.CountryCode)
			}
			geoStr = strings.Join(parts, ", ")
		}

		msg := fmt.Sprintf(".:: Visor Summary ::.\nPublic key: %q\nSymmetric NAT: %t\nLocal IP: %s\nPublic IP: %s\n",
			summary.Overview.PubKey, summary.Overview.IsSymmetricNAT, summary.Overview.LocalIP, summary.Overview.PublicIP)

		if geoStr != "" {
			msg += fmt.Sprintf("Location: %s\n", geoStr)
		}

		msg += fmt.Sprintf("DMSG Servers (%d connected):\n              %s\n", len(summary.ConnectedDmsgServers), dmsgServersStr)
		msg += fmt.Sprintf("DMSG Latency: %s\n", summary.DmsgStats.RoundTrip)
		msg += fmt.Sprintf("Visor Version: %s\nUptime Tracker: %s\nTime Online: %f seconds\nBuild Tag: %s\n",
			summary.Overview.BuildInfo.Version, summary.Health.ServicesHealth, summary.Uptime, summary.BuildTag)

		outputJSON := struct {
			PublicKey            string   `json:"public_key"`
			IsSymmetricNAT       bool     `json:"symmetric_nat"`
			LocalIP              string   `json:"local_ip"`
			PublicIP             string   `json:"public_ip"`
			CountryCode          string   `json:"country_code,omitempty"`
			RegionName           string   `json:"region_name,omitempty"`
			CityName             string   `json:"city_name,omitempty"`
			ConnectedDmsgServers []string `json:"connected_dmsg_servers"`
			DmsgLatency          string   `json:"dmsg_latency"`
			VisorVersion         string   `json:"visor_version"`
			UptimeTracker        string   `json:"uptime_tracker"`
			TimeOnline           float64  `json:"time_online"`
			BuildTag             string   `json:"build_tag"`
		}{
			PublicKey:            summary.Overview.PubKey.String(),
			IsSymmetricNAT:       summary.Overview.IsSymmetricNAT,
			LocalIP:              summary.Overview.LocalIP,
			PublicIP:             summary.Overview.PublicIP,
			CountryCode:          summary.Overview.CountryCode,
			RegionName:           summary.Overview.RegionName,
			CityName:             summary.Overview.CityName,
			ConnectedDmsgServers: summary.ConnectedDmsgServers,
			DmsgLatency:          summary.DmsgStats.RoundTrip.String(),
			VisorVersion:         summary.Overview.BuildInfo.Version,
			UptimeTracker:        summary.Health.ServicesHealth,
			TimeOnline:           summary.Uptime,
			BuildTag:             summary.BuildTag,
		}
		internal.PrintOutput(cmd.Flags(), outputJSON, msg)
	},
}

var buildInfoCmd = &cobra.Command{
	Use:   "ver",
	Short: "Version and build info",
	Long:  "\n  Version and build info",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		overview, err := rpcClient.Overview()
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		buildInfo := overview.BuildInfo
		msg := fmt.Sprintf("Version %q built on %q against commit %q\n", buildInfo.Version, buildInfo.Date, buildInfo.Commit)
		internal.PrintOutput(cmd.Flags(), buildInfo, msg)
	},
}

var portsCmd = &cobra.Command{
	Use:   "ports",
	Short: "List of Ports",
	Long:  "\n  List of all ports used by visor services and apps",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		ports, err := rpcClient.Ports()
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		msg := "+-------------------------------------------+\n"
		msg += fmt.Sprintf("| %-21s | %-7s | %-7s |\n", "App/Service", "Type", "Port")
		msg += "|-------------------------------------------|\n"

		portsName := make([]string, 0, len(ports))
		for portName := range ports {
			portsName = append(portsName, portName)
		}
		sort.Strings(portsName)

		for _, portName := range portsName {
			msg += fmt.Sprintf("| %-21s | %-7s | %7s |\n", portName, ports[portName].Type, ports[portName].Port)
		}

		msg += "|===========================================|\n"
		msg += "| SKYNET: connection between apps and visor |\n"
		msg += "| DMSG: connection by dmsg service          |\n"
		msg += "+-------------------------------------------+\n"
		internal.PrintOutput(cmd.Flags(), ports, msg)
	},
}
