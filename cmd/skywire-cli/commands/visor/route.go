// Package clivisor cmd/skywire-cli/commands/visor/route.go
package clivisor

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
)

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

func init() {
	RootCmd.AddCommand(routeCmd)
	routeCmd.AddCommand(
		lsRulesCmd,
		ruleCmd,
		rmRuleCmd,
		addRuleCmd,
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
	lsRulesCmd.Flags().BoolVarP(&showNextRid, "nrid", "n", false, "display the next available route id")
	//TODO
	//rmRuleCmd.Flags().BoolVarP(&removeAll, "all", "a", false, "remove all routing rules")
}

var routeCmd = &cobra.Command{
	Use:   "route",
	Short: "View and set rules",
	Long:  "\n    View and set routing rules",
}

var ruleCmd = &cobra.Command{
	Use:   "rule <route-id>",
	Short: "Return routing rule matching route ID",
	Long:  "\n    Return routing rule matching route ID",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id, err := strconv.ParseUint(args[0], 10, 32)
		internal.Catch(cmd.Flags(), err)
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		rule, err := rpcClient.RoutingRule(routing.RouteID(id))
		internal.Catch(cmd.Flags(), err)

		printRoutingRules(cmd.Flags(), rule)
	},
}

var lsRulesCmd = &cobra.Command{
	Use:   "ls",
	Short: "List routing rules",
	Long:  "\n    List routing rules",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
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
