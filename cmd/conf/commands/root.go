// Package commands cmd/conf/commands/root.go
package commands

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/deployment"
)

func init() {
	RootCmd.AddCommand(
		ServicesConfCmd,
		DmsghttpConfCmd,
		HTTPConfCmd,
	)
}

// RootCmd is the root command
var RootCmd = &cobra.Command{
	Use:                   "conf",
	Short:                 `print json config files`,
	Long:                  `print json config files`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
}

// ServicesConfCmd is the command to print the full services-config.json
// (both HTTP and DMSG endpoints).
var ServicesConfCmd = &cobra.Command{
	Use:                   "svcconf",
	Short:                 `print services-config.json file`,
	Long:                  `print the full services-config.json (HTTP + DMSG endpoints)`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Printf("%s\n", string(deployment.ServicesJSON))
	},
}

// DmsghttpConfCmd prints the DMSG-only subset of the deployment config.
// HTTP-scheme endpoints are stripped; dmsg:// endpoints and the dmsg
// server list are kept. Intended for use with SKYDEPLOY when forcing
// a visor to reach services only over DMSG.
var DmsghttpConfCmd = &cobra.Command{
	Use:                   "dmsgconf",
	Short:                 `print DMSG-only deployment config`,
	Long:                  `print the DMSG-only subset of services-config.json (http fields stripped)`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(_ *cobra.Command, _ []string) {
		printFilteredJSON(false, true)
	},
}

// HTTPConfCmd prints the HTTP-only subset of the deployment config.
// DMSG endpoints are stripped. Useful when running on an environment
// without DMSG connectivity, or to diagnose HTTP-only fallback
// behavior.
var HTTPConfCmd = &cobra.Command{
	Use:                   "httpconf",
	Short:                 `print HTTP-only deployment config`,
	Long:                  `print the HTTP-only subset of services-config.json (dmsg fields stripped)`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(_ *cobra.Command, _ []string) {
		printFilteredJSON(true, false)
	},
}

// printFilteredJSON marshals the deployment Services filtered to
// HTTP-only, DMSG-only, or both. Identity-only fields (PubKey slices
// like route_setup_nodes, transport_setup, survey_whitelist and the
// dns/stun server lists) are kept in both views since they are not
// scheme-specific. The outer {"prod": ..., "test": ...} wrapper is
// preserved so the output matches the shape of services-config.json.
func printFilteredJSON(keepHTTP, keepDmsg bool) {
	prodFiltered := filterServices(deployment.Prod, keepHTTP, keepDmsg)
	testFiltered := filterServices(deployment.Test, keepHTTP, keepDmsg)

	wrapper := struct {
		Prod deployment.Services `json:"prod"`
		Test deployment.Services `json:"test"`
	}{
		Prod: prodFiltered,
		Test: testFiltered,
	}
	out, err := json.MarshalIndent(wrapper, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s\n", string(out))
}

// filterServices returns a copy of svc with either the http-scheme
// fields or the dmsg-scheme fields (or both) zeroed out. Identity /
// PubKey slices and stun/dns lists are always kept — they are not
// scheme-specific.
func filterServices(svc deployment.Services, keepHTTP, keepDmsg bool) deployment.Services {
	out := svc
	if !keepHTTP {
		out.DmsgDiscovery = ""
		out.TransportDiscovery = ""
		out.AddressResolver = ""
		out.RouteFinder = ""
		out.UptimeTracker = ""
		out.ServiceDiscovery = ""
		out.GeoIP = ""
		out.RewardSystem = ""
	}
	if !keepDmsg {
		out.ConfDmsg = ""
		out.DmsgServers = nil
		out.DmsgDiscoveryDmsg = ""
		out.TransportDiscoveryDmsg = ""
		out.AddressResolverDmsg = ""
		out.RouteFinderDmsg = ""
		out.UptimeTrackerDmsg = ""
		out.ServiceDiscoveryDmsg = ""
		out.RewardSystemDmsg = ""
	}
	return out
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
