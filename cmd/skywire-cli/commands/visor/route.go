package clivisor

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
)

var routeCmd = &cobra.Command{
	Use:   "route",
	Short: "View and set rules",
}

var addRuleCmd = &cobra.Command{
	Use:   "add-rule",
	Short: "Add routing rule",
}

func init() {
	RootCmd.AddCommand(routeCmd)
	addRuleCmd.AddCommand(
		addAppRuleCmd,
		addFwdRuleCmd,
		addIntFwdRuleCmd,
	)
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
	Run: func(cmd *cobra.Command, _ []string) {
		rules, err := clirpc.Client().RoutingRules()
		internal.Catch(cmd.Flags(), err)

		printRoutingRules(cmd.Flags(), rules...)
	},
}

var ruleCmd = &cobra.Command{
	Use:   "rule <route-id>",
	Short: "Return routing rule by route ID key",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id, err := strconv.ParseUint(args[0], 10, 32)
		internal.Catch(cmd.Flags(), err)

		rule, err := clirpc.Client().RoutingRule(routing.RouteID(id))
		internal.Catch(cmd.Flags(), err)

		printRoutingRules(cmd.Flags(), rule)
	},
}

var rmRuleCmd = &cobra.Command{
	Use:   "rm-rule <route-id>",
	Short: "Remove routing rule",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id, err := strconv.ParseUint(args[0], 10, 32)
		internal.Catch(cmd.Flags(), err)
		internal.Catch(cmd.Flags(), clirpc.Client().RemoveRoutingRule(routing.RouteID(id)))
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
	},
}

var keepAlive time.Duration

func init() {
	addRuleCmd.PersistentFlags().DurationVar(&keepAlive, "keep-alive", router.DefaultRouteKeepAlive, "duration after which routing rule will expire if no activity is present")
}

var addAppRuleCmd = &cobra.Command{
	Use:   "app <route-id> <local-pk> <local-port> <remote-pk> <remote-port>",
	Short: "Add app/consume routing rule",
	Args: func(_ *cobra.Command, args []string) error {
		if len(args) > 0 {
			if len(args[0:]) == 5 {
				return nil
			}
			return errors.New("expected 5 args after 'app'")
		}
		return errors.New("expected 5 args after 'app'")
	},
	Run: func(cmd *cobra.Command, args []string) {
		var rule routing.Rule
		var (
			routeID    = routing.RouteID(parseUint(cmd.Flags(), "route-id", args[0], 32))
			localPK    = internal.ParsePK(cmd.Flags(), "local-pk", args[1])
			localPort  = routing.Port(parseUint(cmd.Flags(), "local-port", args[2], 16))
			remotePK   = internal.ParsePK(cmd.Flags(), "remote-pk", args[3])
			remotePort = routing.Port(parseUint(cmd.Flags(), "remote-port", args[4], 16))
		)
		rule = routing.ConsumeRule(keepAlive, routeID, localPK, remotePK, localPort, remotePort)

		var rIDKey routing.RouteID
		if rule != nil {
			rIDKey = rule.KeyRouteID()
		}

		internal.Catch(cmd.Flags(), clirpc.Client().SaveRoutingRule(rule))

		output := struct {
			RoutingRuleKey routing.RouteID `json:"routing_route_key"`
		}{
			RoutingRuleKey: rIDKey,
		}

		internal.PrintOutput(cmd.Flags(), output, fmt.Sprintf("Routing Rule Key: %v\n", rIDKey))
	},
}

var addFwdRuleCmd = &cobra.Command{
	Use:   "fwd <route-id> <next-route-id> <next-transport-id> <local-pk> <local-port> <remote-pk> <remote-port>",
	Short: "Add forward routing rule",
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

		internal.Catch(cmd.Flags(), clirpc.Client().SaveRoutingRule(rule))

		output := struct {
			RoutingRuleKey routing.RouteID `json:"routing_route_key"`
		}{
			RoutingRuleKey: rIDKey,
		}

		internal.PrintOutput(cmd.Flags(), output, fmt.Sprintf("Routing Rule Key: %v\n", rIDKey))
	},
}

var addIntFwdRuleCmd = &cobra.Command{
	Use:   "intfwd <route-id> <next-route-id> <next-transport-id>",
	Short: "Add intermediary forward routing rule",
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

		internal.Catch(cmd.Flags(), clirpc.Client().SaveRoutingRule(rule))

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
		internal.PrintError(cmdFlags, fmt.Errorf("failed to parse <%s>: %v", name, err))
	}
	return i
}
