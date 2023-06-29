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

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
)

var routeCmd = &cobra.Command{
	Use:   "route",
	Short: "View and set rules",
	Long:  "\n    View and set routing rules",
}

func init() {
	RootCmd.AddCommand(routeCmd)
	routeCmd.AddCommand(
		lsRulesCmd,
		ruleCmd,
		rmRuleCmd,
		addRuleCmd,
	)
}

var lsRulesCmd = &cobra.Command{
	Use:   "ls-rules",
	Short: "List routing rules",
	Long:  "\n    List routing rules",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		rules, err := rpcClient.RoutingRules()
		internal.Catch(cmd.Flags(), err)

		printRoutingRules(cmd.Flags(), rules...)
	},
}

var ruleCmd = &cobra.Command{
	Use:   "rule <route-id>",
	Short: "Return routing rule by route ID key",
	Long:  "\n    Return routing rule by route ID key",
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

func init() {
	rmRuleCmd.Flags().BoolVarP(&removeAll, "all", "a", false, "remove all routing rules")
}

var rmRuleCmd = &cobra.Command{
	Use:   "rm-rule <route-id>",
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
	Use:   "add-rule ( app | fwd | intfwd )",
	Short: "Add routing rule",
	Long:  "\n    Add routing rule",
}

var keepAlive time.Duration

var (
	nrID  string
	ntpID string
	rID   string
	lPK   string
	lPt   string
	rPK   string
	rPt   string
)

//skywire-cli visor route add-rule app

func init() {
	addRuleCmd.PersistentFlags().DurationVar(&keepAlive, "keep-alive", router.DefaultRouteKeepAlive, "timeout for rule expiration")
	addRuleCmd.AddCommand(
		addAppRuleCmd,
	)
	addAppRuleCmd.Flags().SortFlags = false
	addAppRuleCmd.Flags().StringVarP(&rID, "rid", "i", "", "route id")
	addAppRuleCmd.Flags().StringVarP(&lPK, "lpk", "l", "", "local public key")
	addAppRuleCmd.Flags().StringVarP(&lPt, "lpt", "m", "", "local port")
	addAppRuleCmd.Flags().StringVarP(&rPK, "rpk", "p", "", "remote pk")
	addAppRuleCmd.Flags().StringVarP(&rPt, "rpt", "q", "", "remote port")
}

var addAppRuleCmd = &cobra.Command{
	Use:   "app \\\n               <route-id> \\\n               <local-pk> \\\n               <local-port> \\\n               <remote-pk> \\\n               <remote-port> \\\n               || ",
	Short: "Add app/consume routing rule",
	Long:  "\n    Add app/consume routing rule",
	Args: func(_ *cobra.Command, args []string) error {
		if rID == "" && lPK == "" && lPt == "" && rPK == "" && rPt == "" {
			if len(args) > 0 {
				if len(args[0:]) == 5 {
					return nil
				}
				return errors.New("expected 5 args after 'app'")
			}
			return errors.New("expected 5 args after 'app'")
		}
		return nil
	},
	Run: func(cmd *cobra.Command, args []string) {
		var (
			rule       routing.Rule
			routeID    routing.RouteID
			localPK    cipher.PubKey
			localPort  routing.Port
			remotePK   cipher.PubKey
			remotePort routing.Port
		)
		//use args if flags are empty strings
		if rID == "" && lPK == "" && lPt == "" && rPK == "" && rPt == "" {
			routeID = routing.RouteID(parseUint(cmd.Flags(), "route-id", args[0], 32))
			localPK = internal.ParsePK(cmd.Flags(), "local-pk", args[1])
			localPort = routing.Port(parseUint(cmd.Flags(), "local-port", args[2], 16))
			remotePK = internal.ParsePK(cmd.Flags(), "remote-pk", args[3])
			remotePort = routing.Port(parseUint(cmd.Flags(), "remote-port", args[4], 16))
		} else {
			//the presence of every flag is enforced on an individual basis
			if rID != "" {
				i, err := strconv.ParseUint(rID, 10, 32)
				if err != nil {
					internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to parse <%s>: %v", rID, err))
				}
				routeID = routing.RouteID(i)
			} else {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("required flag not specified"))
			}

			if lPK != "" {
				internal.Catch(cmd.Flags(), localPK.Set(lPK))
			} else {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("required flag not specified"))
			}

			if lPt != "" {
				i, err := strconv.ParseUint(lPt, 10, 16)
				if err != nil {
					internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to parse <%s>: %v", lPt, err))
				}
				localPort = routing.Port(i)
			} else {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("required flag not specified"))
			}

			if rPK != "" {
				internal.Catch(cmd.Flags(), remotePK.Set(rPK))
			} else {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("required flag not specified"))
			}

			if rPt != "" {
				i, err := strconv.ParseUint(rPt, 10, 16)
				if err != nil {
					internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to parse <%s>: %v", rPt, err))
				}
				localPort = routing.Port(i)
			} else {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("required flag not specified"))
			}
		}

		rule = routing.ConsumeRule(keepAlive, routeID, localPK, remotePK, localPort, remotePort)
		var rIDKey routing.RouteID
		if rule != nil {
			rIDKey = rule.KeyRouteID()
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		internal.Catch(cmd.Flags(), rpcClient.SaveRoutingRule(rule))

		output := struct {
			RoutingRuleKey routing.RouteID `json:"routing_route_key"`
		}{
			RoutingRuleKey: rIDKey,
		}

		internal.PrintOutput(cmd.Flags(), output, fmt.Sprintf("Routing Rule Key: %v\n", rIDKey))
	},
}

//skywire-cli visor route add-rule fwd

func init() {
	addRuleCmd.AddCommand(
		addFwdRuleCmd,
	)
	addFwdRuleCmd.Flags().SortFlags = false
	addFwdRuleCmd.Flags().StringVarP(&rID, "rid", "i", "", "route id")
	addFwdRuleCmd.Flags().StringVarP(&nrID, "nrid", "j", "", "next route id")
	addFwdRuleCmd.Flags().StringVarP(&ntpID, "ntpid", "k", "", "next transport id")
	addFwdRuleCmd.Flags().StringVarP(&lPK, "lpk", "l", "", "local public key")
	addFwdRuleCmd.Flags().StringVarP(&lPt, "lpt", "m", "", "local port")
	addFwdRuleCmd.Flags().StringVarP(&rPK, "rpk", "p", "", "remote pk")
	addFwdRuleCmd.Flags().StringVarP(&rPt, "rpt", "q", "", "remote port")
}

var addFwdRuleCmd = &cobra.Command{
	Use:   "fwd \\\n               <route-id> \\\n               <next-route-id> \\\n               <next-transport-id> \\\n               <local-pk> \\\n               <local-port> \\\n               <remote-pk> \\\n               <remote-port> \\\n               || ",
	Short: "Add forward routing rule",
	Long:  "\n    Add forward routing rule",
	Args: func(_ *cobra.Command, args []string) error {
		if len(args) > 0 {
			if len(args[1:]) == 6 {
				return nil
			}
			return errors.New("expected 6 args after 'fwd'")
		}
		return errors.New("expected 6 args after 'fwd'")
	},
	Run: func(cmd *cobra.Command, args []string) {
		var rule routing.Rule
		var (
			routeID     = routing.RouteID(parseUint(cmd.Flags(), "route-id", args[0], 32))
			nextRouteID = routing.RouteID(parseUint(cmd.Flags(), "next-route-id", args[1], 32))
			nextTpID    = internal.ParseUUID(cmd.Flags(), "next-transport-id", args[2])
			localPK     = internal.ParsePK(cmd.Flags(), "local-pk", args[3])
			localPort   = routing.Port(parseUint(cmd.Flags(), "local-port", args[4], 16))
			remotePK    = internal.ParsePK(cmd.Flags(), "remote-pk", args[5])
			remotePort  = routing.Port(parseUint(cmd.Flags(), "remote-port", args[6], 16))
		)
		rule = routing.ForwardRule(keepAlive, routeID, nextRouteID, nextTpID, localPK, remotePK, localPort, remotePort)
		var rIDKey routing.RouteID
		if rule != nil {
			rIDKey = rule.KeyRouteID()
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		internal.Catch(cmd.Flags(), rpcClient.SaveRoutingRule(rule))

		output := struct {
			RoutingRuleKey routing.RouteID `json:"routing_route_key"`
		}{
			RoutingRuleKey: rIDKey,
		}

		internal.PrintOutput(cmd.Flags(), output, fmt.Sprintf("Routing Rule Key: %v\n", rIDKey))
	},
}

//skywire-cli visor route add-rule intfwd

func init() {
	addRuleCmd.AddCommand(
		addIntFwdRuleCmd,
	)
	addIntFwdRuleCmd.Flags().SortFlags = false
	addIntFwdRuleCmd.Flags().StringVarP(&rID, "rid", "i", "", "route id")
	addIntFwdRuleCmd.Flags().StringVarP(&nrID, "nrid", "n", "", "next route id")
	addIntFwdRuleCmd.Flags().StringVarP(&rPt, "tpid", "t", "", "next transport id")
}

var addIntFwdRuleCmd = &cobra.Command{
	Use:   "intfwd \\\n               <route-id> \\\n               <next-route-id> \\\n               <next-transport-id> \\\n               || ",
	Short: "Add intermediary forward routing rule",
	Long:  "\n    Add intermediary forward routing rule",
	Args: func(_ *cobra.Command, args []string) error {
		if len(args) > 0 {
			if len(args[0:]) == 3 {
				return nil
			}
			return errors.New("expected 3 args after 'intfwd'")
		}
		return errors.New("expected 3 args after 'intfwd'")
	},
	Run: func(cmd *cobra.Command, args []string) {
		var rule routing.Rule
		var (
			routeID     = routing.RouteID(parseUint(cmd.Flags(), "route-id", args[0], 32))
			nextRouteID = routing.RouteID(parseUint(cmd.Flags(), "next-route-id", args[1], 32))
			nextTpID    = internal.ParseUUID(cmd.Flags(), "next-transport-id", args[2])
		)
		rule = routing.IntermediaryForwardRule(keepAlive, routeID, nextRouteID, nextTpID)
		var rIDKey routing.RouteID
		if rule != nil {
			rIDKey = rule.KeyRouteID()
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		internal.Catch(cmd.Flags(), rpcClient.SaveRoutingRule(rule))

		output := struct {
			RoutingRuleKey routing.RouteID `json:"routing_route_key"`
		}{
			RoutingRuleKey: rIDKey,
		}

		internal.PrintOutput(cmd.Flags(), output, fmt.Sprintf("Routing Rule Key: %v\n", rIDKey))
	},
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
