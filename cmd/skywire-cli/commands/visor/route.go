package clivisor

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
)

var routeCmd = &cobra.Command{
	Use:   "route",
	Short: "View and set rules",
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
	Run: func(_ *cobra.Command, _ []string) {
		rules, err := clirpc.Client().RoutingRules()
		internal.Catch(err)

		printRoutingRules(rules...)
	},
}

var ruleCmd = &cobra.Command{
	Use:   "rule <route-id>",
	Short: "Return routing rule by route ID key",
	Args:  cobra.MinimumNArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		id, err := strconv.ParseUint(args[0], 10, 32)
		internal.Catch(err)

		rule, err := clirpc.Client().RoutingRule(routing.RouteID(id))
		internal.Catch(err)

		printRoutingRules(rule)
	},
}

var rmRuleCmd = &cobra.Command{
	Use:   "rm-rule <route-id>",
	Short: "Remove routing rule",
	Args:  cobra.MinimumNArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		id, err := strconv.ParseUint(args[0], 10, 32)
		internal.Catch(err)
		internal.Catch(clirpc.Client().RemoveRoutingRule(routing.RouteID(id)))
		fmt.Println("OK")
	},
}

var keepAlive time.Duration

func init() {
	addRuleCmd.PersistentFlags().DurationVar(&keepAlive, "keep-alive", router.DefaultRouteKeepAlive, "duration after which routing rule will expire if no activity is present")
}

var addRuleCmd = &cobra.Command{
	Use:   "add-rule (app <route-id> <local-pk> <local-port> <remote-pk> <remote-port> | fwd <next-route-id> <next-transport-id>)",
	Short: "Add routing rule",
	Args: func(_ *cobra.Command, args []string) error {
		if len(args) > 0 {
			switch rt := args[0]; rt {
			case "app":
				if len(args[0:]) == 4 {
					return nil
				}
				return errors.New("expected 4 args after 'app'")
			case "fwd":
				if len(args[0:]) == 2 {
					return nil
				}
				return errors.New("expected 2 args after 'fwd'")
			}
		}
		return errors.New("expected 'app' or 'fwd' after 'add-rule'")
	},
	Run: func(_ *cobra.Command, args []string) {
		var rule routing.Rule
		switch args[0] {
		case "app":
			var (
				routeID    = routing.RouteID(parseUint("route-id", args[1], 32))
				localPK    = internal.ParsePK("local-pk", args[2])
				localPort  = routing.Port(parseUint("local-port", args[3], 16))
				remotePK   = internal.ParsePK("remote-pk", args[4])
				remotePort = routing.Port(parseUint("remote-port", args[5], 16))
			)
			rule = routing.ConsumeRule(keepAlive, routeID, localPK, remotePK, localPort, remotePort)
		case "fwd":
			var (
				nextRouteID = routing.RouteID(parseUint("next-route-id", args[1], 32))
				nextTpID    = internal.ParseUUID("next-transport-id", args[2])
			)
			rule = routing.IntermediaryForwardRule(keepAlive, 0, nextRouteID, nextTpID)
		}
		var rIDKey routing.RouteID
		if rule != nil {
			rIDKey = rule.KeyRouteID()
		}

		internal.Catch(clirpc.Client().SaveRoutingRule(rule))
		fmt.Println("Routing Rule Key:", rIDKey)
	},
}

func printRoutingRules(rules ...routing.Rule) {
	printConsumeRule := func(w io.Writer, id routing.RouteID, s *routing.RuleSummary) {
		_, err := fmt.Fprintf(w, "%d\t%s\t%d\t%d\t%s\t%s\t%s\t%s\t%s\n", id, s.Type,
			s.ConsumeFields.RouteDescriptor.SrcPort, s.ConsumeFields.RouteDescriptor.DstPort,
			s.ConsumeFields.RouteDescriptor.DstPK, "-", "-", "-", s.KeepAlive)
		internal.Catch(err)
	}
	printFwdRule := func(w io.Writer, id routing.RouteID, s *routing.RuleSummary) {
		_, err := fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n", id, s.Type, "-",
			"-", "-", "-", s.ForwardFields.NextRID, s.ForwardFields.NextTID, s.KeepAlive)
		internal.Catch(err)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 5, ' ', tabwriter.TabIndent)
	_, err := fmt.Fprintln(w, "id\ttype\tlocal-port\tremote-port\tremote-pk\tresp-id\tnext-route-id\tnext-transport-id\texpire-at")
	internal.Catch(err)
	for _, rule := range rules {
		if rule.Summary().ConsumeFields != nil {
			printConsumeRule(w, rule.KeyRouteID(), rule.Summary())
		} else {
			printFwdRule(w, rule.NextRouteID(), rule.Summary())
		}
	}
	internal.Catch(w.Flush())
}

func parseUint(name, v string, bitSize int) uint64 {
	i, err := strconv.ParseUint(v, 10, bitSize)
	internal.Catch(err, fmt.Sprintf("failed to parse <%s>:", name))
	return i
}
