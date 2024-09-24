// Package cliroute cmd/skywire-cli/commands/route/route.go
package cliroute

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/net/context"

	"github.com/skycoin/skywire"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/routefinder/rfclient"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var (
	frAddr               string
	frMinHops, frMaxHops uint16
	timeout              time.Duration
	skywireconfig        string
	rfURL                string
)

func init() {
	var envServices skywire.EnvServices
	var services skywire.Services
	if err := json.Unmarshal([]byte(skywire.ServicesJSON), &envServices); err == nil {
		if err := json.Unmarshal(envServices.Prod, &services); err == nil {
			rfURL = services.RouteFinder
		}
	}
	findCmd.Flags().SortFlags = false
	findCmd.Flags().Uint16VarP(&frMinHops, "min", "n", 1, "minimum hops")
	findCmd.Flags().Uint16VarP(&frMaxHops, "max", "x", 1000, "maximum hops")
	findCmd.Flags().DurationVarP(&timeout, "timeout", "t", 10*time.Second, "request timeout")
	findCmd.Flags().StringVarP(&frAddr, "addr", "a", rfURL, "route finder service address")
}

// RootCmd is the command that queries the route finder.
var findCmd = &cobra.Command{
	Use:   "find <public-key> | <public-key-visor-1> <public-key-visor-2>",
	Short: "Query the Route Finder",
	Long: `Query the Route Finder
Assumes the local visor public key as an argument if only one argument is given`,
	// Args: cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var srcPK, dstPK cipher.PubKey
		var pk string
		//print the help menu if no arguments
		if len(args) == 0 {
			cmd.Help() //nolint
			os.Exit(0)
		}
		//set the routefinder address. It's not used as the default value to fix the display of the help command
		if frAddr == "" {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Route Finder URL not specified"))
		}
		//assume the local public key as the first argument if only 1 argument is given ; resize args array to 2 and move the first argument to the second one
		if len(args) == 1 {
			rpcClient, err := clirpc.Client(cmd.Flags())
			if err == nil {
				overview, err := rpcClient.Overview()
				if err == nil {
					pk = overview.PubKey.String()
				}
			}
			if err != nil {
				//visor is not running, try to get pk from config
				_, err := os.Stat(skyenv.SkywirePath + "/" + skyenv.ConfigJSON)
				if err == nil {
					//default to using the package config
					skywireconfig = skyenv.SkywirePath + "/" + skyenv.ConfigJSON
				} else {
					//check for default config in current dir
					_, err := os.Stat(skyenv.ConfigName)
					if err == nil {
						//use skywire-config.json in current dir
						skywireconfig = skyenv.ConfigName
					}
				}
				conf, err := visorconfig.ReadFile(skywireconfig)
				if err != nil {
					internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to read config: %v", err))
				}
				pk = conf.PK.Hex()
			}
			args = append(args[:1], args[0:]...)
			copy(args, []string{pk})
		}
		rfc := rfclient.NewHTTP(frAddr, timeout, &http.Client{}, nil)
		internal.Catch(cmd.Flags(), srcPK.Set(args[0]))
		internal.Catch(cmd.Flags(), dstPK.Set(args[1]))
		forward := [2]cipher.PubKey{srcPK, dstPK}
		backward := [2]cipher.PubKey{dstPK, srcPK}
		ctx := context.Background()
		routes, err := rfc.FindRoutes(ctx, []routing.PathEdges{forward, backward},
			&rfclient.RouteOptions{MinHops: frMinHops, MaxHops: frMaxHops})
		internal.Catch(cmd.Flags(), err)
		output := fmt.Sprintf("forward: %v\n reverse: %v", routes[forward][0], routes[backward][0])
		outputJSON := struct {
			Forward []routing.Hop `json:"forward"`
			Reverse []routing.Hop `json:"reverse"`
		}{
			Forward: routes[forward][0],
			Reverse: routes[backward][0],
		}
		internal.PrintOutput(cmd.Flags(), outputJSON, output)
	},
}
var (
	nrID        string
	ntpID       string
	rID         string
	lPK         string
	lPt         string
	rPK         string
	rPt         string
	rule        routing.Rule
	routeID     routing.RouteID
	nextRouteID routing.RouteID
	nextTpID    uuid.UUID
	localPK     cipher.PubKey
	localPort   routing.Port
	remotePK    cipher.PubKey
	remotePort  routing.Port
	keepAlive   time.Duration
	showNextRid bool
)

// RootCmd is routeCmd
var RootCmd = routeCmd

func init() {
	routeCmd.AddCommand(
		rmRuleCmd,
		addRuleCmd,
		findCmd,
	)
	addRuleCmd.PersistentFlags().DurationVarP(&keepAlive, "keep-alive", "a", router.DefaultRouteKeepAlive, "timeout for rule expiration")
	addRuleCmd.AddCommand(
		appRuleCmd,
		intFwdRuleCmd,
		fwdRuleCmd,
	)
	appRuleCmd.Flags().SortFlags = false
	fwdRuleCmd.Flags().SortFlags = false
	intFwdRuleCmd.Flags().SortFlags = false

	appRuleCmd.Flags().StringVarP(&rID, "rid", "i", "", "route id")
	intFwdRuleCmd.Flags().StringVarP(&rID, "rid", "i", "", "route id")
	fwdRuleCmd.Flags().StringVarP(&rID, "rid", "i", "", "route id")
	intFwdRuleCmd.Flags().StringVarP(&nrID, "nrid", "j", "", "next route id")
	fwdRuleCmd.Flags().StringVarP(&nrID, "nrid", "j", "", "next route id")
	intFwdRuleCmd.Flags().StringVarP(&ntpID, "tpid", "k", "", "next transport id")
	fwdRuleCmd.Flags().StringVarP(&ntpID, "tpid", "k", "", "next transport id")
	appRuleCmd.Flags().StringVarP(&lPK, "lpk", "l", "", "local public key")
	fwdRuleCmd.Flags().StringVarP(&lPK, "lpk", "l", "", "local public key")
	appRuleCmd.Flags().StringVarP(&lPt, "lpt", "m", "", "local port")
	fwdRuleCmd.Flags().StringVarP(&lPt, "lpt", "m", "", "local port")
	appRuleCmd.Flags().StringVarP(&rPK, "rpk", "p", "", "remote pk")
	fwdRuleCmd.Flags().StringVarP(&rPK, "rpk", "p", "", "remote pk")
	appRuleCmd.Flags().StringVarP(&rPt, "rpt", "q", "", "remote port")
	fwdRuleCmd.Flags().StringVarP(&rPt, "rpt", "q", "", "remote port")
	routeCmd.Flags().BoolVarP(&showNextRid, "nrid", "n", false, "display the next available route id")
	routeCmd.Flags().StringVarP(&rID, "rid", "i", "", "show routing rule matching route ID")
	//TODO
	//rmRuleCmd.Flags().BoolVarP(&removeAll, "all", "a", false, "remove all routing rules")
}

var routeCmd = &cobra.Command{
	Use:   "route",
	Short: "View and set rules",
	Long:  "\n    View and set routing rules",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if rID != "" {
			rule, err := rpcClient.RoutingRule(routing.RouteID(parseUint(cmd.Flags(), "route id flag value -i --rid", rID, 32)))
			internal.Catch(cmd.Flags(), err)
			printRoutingRules(cmd.Flags(), rule)
			return
		}
		rules, err := rpcClient.RoutingRules()
		internal.Catch(cmd.Flags(), err)
		if showNextRid {
			fmt.Println(getNextAvailableRouteID(rules...))
		} else {
			printRoutingRules(cmd.Flags(), rules...)
		}
	},
}

var rmRuleCmd = &cobra.Command{
	Use:   "rm <route-id>",
	Short: "Remove routing rule",
	Long:  "\n    Remove routing rule",
	//Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		//TODO
		//if removeAll {
		//rules, err := clirpc.Client(cmd.Flags()).RoutingRules()
		//internal.Catch(cmd.Flags(), err)
		//internal.Catch(cmd.Flags(), clirpc.Client(cmd.Flags()).RemoveRoutingRule(routing.RouteID(rules...)))
		//} else {
		id, err := strconv.ParseUint(args[0], 10, 32)
		internal.Catch(cmd.Flags(), err)
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		internal.Catch(cmd.Flags(), rpcClient.RemoveRoutingRule(routing.RouteID(id)))
		//}
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
	},
}

var addRuleCmd = &cobra.Command{
	Use:   "add ( app | fwd | intfwd )",
	Short: "Add routing rule",
	Long:  "\n    Add routing rule",
}

var appRuleCmd = &cobra.Command{
	Use:   "a",
	Short: "Add app/consume routing rule",
	Long:  "\n    Add app/consume routing rule",
	PreRun: func(cmd *cobra.Command, _ []string) {
		if rID == "" {
			internal.PrintFatalError(cmd.Flags(), errors.New("missing route id flag value -i --rid"))
		}
		if lPK == "" {
			internal.PrintFatalError(cmd.Flags(), errors.New("missing local public key flag value -l --lpk"))
		}
		if lPt == "" {
			internal.PrintFatalError(cmd.Flags(), errors.New("missing local port flag value -m --lpt"))
		}
		if rPK == "" {
			internal.PrintFatalError(cmd.Flags(), errors.New("missing remote pk flag value -p --rpk"))
		}
		if rPt == "" {
			internal.PrintFatalError(cmd.Flags(), errors.New("missing remote port flag value -q --rpt"))
		}
	},
	Run: func(cmd *cobra.Command, _ []string) {
		routeID = routing.RouteID(parseUint(cmd.Flags(), "route id flag value -i --rid", rID, 32))
		localPK = internal.ParsePK(cmd.Flags(), "local public key flag value -l --lpk", lPK)
		localPort = routing.Port(parseUint(cmd.Flags(), "local port flag value -m --lpt", lPt, 16))
		remotePK = internal.ParsePK(cmd.Flags(), "remote pk flag value -p --rpk", rPK)
		remotePort = routing.Port(parseUint(cmd.Flags(), "remote port flag value -q --rpt", rPt, 16))
		rule = routing.ConsumeRule(keepAlive, routeID, localPK, remotePK, localPort, remotePort)

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		internal.Catch(cmd.Flags(), rpcClient.SaveRoutingRule(rule))

		output := struct {
			RoutingRuleKey routing.RouteID `json:"routing_route_key"`
		}{
			RoutingRuleKey: routeID,
		}

		internal.PrintOutput(cmd.Flags(), output, fmt.Sprintf("Routing Rule Key: %v\n", routeID))
	},
}

var fwdRuleCmd = &cobra.Command{
	Use:   "c",
	Short: "Add forward routing rule",
	Long:  "\n    Add forward routing rule",
	PreRun: func(cmd *cobra.Command, _ []string) {
		if rID == "" {
			internal.PrintFatalError(cmd.Flags(), errors.New("missing route id flag value -i --rid"))
		}
		if nrID == "" {
			internal.PrintFatalError(cmd.Flags(), errors.New("missing next route id flag value -j --nrid"))
		}
		if ntpID == "" {
			internal.PrintFatalError(cmd.Flags(), errors.New("missing next transport id flag value -k --tpid"))
		}
		if lPK == "" {
			internal.PrintFatalError(cmd.Flags(), errors.New("missing local public key flag value -l --lpk"))
		}
		if lPt == "" {
			internal.PrintFatalError(cmd.Flags(), errors.New("missing local port flag value -m --lpt"))
		}
		if rPK == "" {
			internal.PrintFatalError(cmd.Flags(), errors.New("missing remote pk flag value -p --rpk"))
		}
		if rPt == "" {
			internal.PrintFatalError(cmd.Flags(), errors.New("missing remote port flag value -q --rpt"))
		}
	},
	Run: func(cmd *cobra.Command, _ []string) {
		routeID = routing.RouteID(parseUint(cmd.Flags(), "route id flag value -i --rid", rID, 32))
		nextRouteID = routing.RouteID(parseUint(cmd.Flags(), "next route id flag value -j --nrid", nrID, 32))
		nextTpID = internal.ParseUUID(cmd.Flags(), "next transport id flag value -k --tpid", ntpID)
		localPK = internal.ParsePK(cmd.Flags(), "local public key flag value -l --lpk", lPK)
		localPort = routing.Port(parseUint(cmd.Flags(), "local port flag value -m --lpt", lPt, 16))
		remotePK = internal.ParsePK(cmd.Flags(), "remote pk flag value -p --rpk", rPK)
		remotePort = routing.Port(parseUint(cmd.Flags(), "remote port flag value -q --rpt", rPt, 16))
		rule = routing.ForwardRule(keepAlive, routeID, nextRouteID, nextTpID, localPK, remotePK, localPort, remotePort)

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		internal.Catch(cmd.Flags(), rpcClient.SaveRoutingRule(rule))

		output := struct {
			RoutingRuleKey routing.RouteID `json:"routing_route_key"`
		}{
			RoutingRuleKey: routeID,
		}

		internal.PrintOutput(cmd.Flags(), output, fmt.Sprintf("Routing Rule Key: %v\n", routeID))
	},
}

var intFwdRuleCmd = &cobra.Command{
	Use:   "b",
	Short: "Add intermediary forward routing rule",
	Long:  "\n    Add intermediary forward routing rule",
	PreRun: func(cmd *cobra.Command, _ []string) {
		if rID == "" {
			internal.PrintFatalError(cmd.Flags(), errors.New("missing route id flag value -i --rid"))
		}
		if nrID == "" {
			internal.PrintFatalError(cmd.Flags(), errors.New("missing next route id flag value -j --nrid"))
		}
		if ntpID == "" {
			internal.PrintFatalError(cmd.Flags(), errors.New("missing next transport id flag value -k --tpid"))
		}
	},
	Run: func(cmd *cobra.Command, _ []string) {
		routeID = routing.RouteID(parseUint(cmd.Flags(), "route id flag value -i --rid", rID, 32))
		nextRouteID = routing.RouteID(parseUint(cmd.Flags(), "next route id flag value -j --nrid", nrID, 32))
		nextTpID = internal.ParseUUID(cmd.Flags(), "next transport id flag value -k --tpid", ntpID)

		rule = routing.IntermediaryForwardRule(keepAlive, routeID, nextRouteID, nextTpID)
		if rule != nil {
			routeID = rule.KeyRouteID()
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		internal.Catch(cmd.Flags(), rpcClient.SaveRoutingRule(rule))

		output := struct {
			RoutingRuleKey routing.RouteID `json:"routing_route_key"`
		}{
			RoutingRuleKey: routeID,
		}

		internal.PrintOutput(cmd.Flags(), output, fmt.Sprintf("Routing Rule Key: %v\n", routeID))
	},
}

func getNextAvailableRouteID(rules ...routing.Rule) routing.RouteID {
	for _, rule := range rules {
		id := rule.KeyRouteID()
		if id > routeID {
			routeID = id
		}
	}
	return routeID + 1
}

func printRoutingRules(cmdFlags *pflag.FlagSet, rules ...routing.Rule) {

	type jsonRule struct {
		ID          routing.RouteID `json:"id"`
		Type        string          `json:"type"`
		LocalPort   string          `json:"local_port,omitempty"`
		RemotePort  string          `json:"remote_port,omitempty"`
		RemotePK    string          `json:"remote_pk,omitempty"`
		NextRouteID string          `json:"next_route_id,omitempty"`
		NextTpID    string          `json:"next_transport_id,omitempty"`
		ExpireAt    time.Duration   `json:"expire-at"`
	}

	var jsonRules []jsonRule

	printConsumeRule := func(w io.Writer, id routing.RouteID, s *routing.RuleSummary) {
		_, err := fmt.Fprintf(w, "%d\t%s\t%d\t%d\t%s\t%s\t%s\t%s\n", id, s.Type,
			s.ConsumeFields.RouteDescriptor.SrcPort, s.ConsumeFields.RouteDescriptor.DstPort,
			s.ConsumeFields.RouteDescriptor.DstPK, "-", "-", s.KeepAlive)

		jRule := jsonRule{
			ID:          id,
			Type:        s.Type.String(),
			LocalPort:   fmt.Sprint(s.ConsumeFields.RouteDescriptor.SrcPort),
			RemotePort:  fmt.Sprint(s.ConsumeFields.RouteDescriptor.DstPort),
			RemotePK:    s.ConsumeFields.RouteDescriptor.DstPK.Hex(),
			NextRouteID: "-",
			NextTpID:    "-",
			ExpireAt:    s.KeepAlive,
		}
		jsonRules = append(jsonRules, jRule)
		internal.Catch(cmdFlags, err)
	}

	printFwdRule := func(w io.Writer, id routing.RouteID, s *routing.RuleSummary) {
		_, err := fmt.Fprintf(w, "%d\t%s\t%d\t%d\t%s\t%d\t%s\t%s\n", id, s.Type, s.ForwardFields.RouteDescriptor.SrcPort,
			s.ForwardFields.RouteDescriptor.DstPort, s.ForwardFields.RouteDescriptor.DstPK, s.ForwardFields.NextRID, s.ForwardFields.NextTID, s.KeepAlive)

		jRule := jsonRule{
			ID:          id,
			Type:        s.Type.String(),
			LocalPort:   fmt.Sprint(s.ForwardFields.RouteDescriptor.SrcPort),
			RemotePort:  fmt.Sprint(s.ForwardFields.RouteDescriptor.DstPort),
			RemotePK:    s.ForwardFields.RouteDescriptor.DstPK.Hex(),
			NextRouteID: fmt.Sprint(s.ForwardFields.NextRID),
			NextTpID:    s.ForwardFields.NextTID.String(),
			ExpireAt:    s.KeepAlive,
		}
		jsonRules = append(jsonRules, jRule)
		internal.Catch(cmdFlags, err)
	}

	printIntFwdRule := func(w io.Writer, id routing.RouteID, s *routing.RuleSummary) {
		_, err := fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n", id, s.Type, "-",
			"-", "-", s.IntermediaryForwardFields.NextRID, s.IntermediaryForwardFields.NextTID, s.KeepAlive)

		jRule := jsonRule{
			ID:          id,
			Type:        s.Type.String(),
			LocalPort:   "-",
			RemotePort:  "-",
			RemotePK:    "-",
			NextRouteID: fmt.Sprint(s.IntermediaryForwardFields.NextRID),
			NextTpID:    s.IntermediaryForwardFields.NextTID.String(),
			ExpireAt:    s.KeepAlive,
		}
		jsonRules = append(jsonRules, jRule)
		internal.Catch(cmdFlags, err)
	}

	var b bytes.Buffer
	w := tabwriter.NewWriter(&b, 0, 0, 5, ' ', tabwriter.TabIndent)
	_, err := fmt.Fprintln(w, "id\ttype\tlocal-port\tremote-port\tremote-pk\tnext-route-id\tnext-transport-id\texpire-at")
	internal.Catch(cmdFlags, err)
	for _, rule := range rules {
		if rule.Summary().ConsumeFields != nil {
			printConsumeRule(w, rule.KeyRouteID(), rule.Summary())
			continue
		}
		if rule.Summary().Type == routing.RuleForward {
			printFwdRule(w, rule.NextRouteID(), rule.Summary())
			continue
		}
		printIntFwdRule(w, rule.NextRouteID(), rule.Summary())

	}
	internal.Catch(cmdFlags, w.Flush())
	internal.PrintOutput(cmdFlags, jsonRules, b.String())
}

func parseUint(cmdFlags *pflag.FlagSet, name, v string, bitSize int) uint64 {
	i, err := strconv.ParseUint(v, 10, bitSize)
	if err != nil {
		internal.PrintFatalError(cmdFlags, fmt.Errorf("failed to parse <%s>: %v", name, err))
	}
	return i
}
