// Package net cmd/skywire-cli/commands/net/publish.go
package net

import (
	"bytes"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/app/appnet"
)

var (
	netType string
	skyPort int
)

func init() {
	pubCmd.PersistentFlags().IntVarP(&localPort, "port", "p", 0, "local port of the external (http, tcp, udp) app")
	pubCmd.PersistentFlags().IntVarP(&skyPort, "skyport", "s", localPort, "skywire port for the external (http, tcp, udp) app")
	pubCmd.PersistentFlags().StringVarP(&netType, "type", "t", "http", "type of the external app connection (http, tcp, udp)")

	pubCmd.AddCommand(lsPubCmd)
	pubCmd.AddCommand(stopPubCmd)
	RootCmd.AddCommand(pubCmd)
}

// pubCmd contains commands to publish over the skywire network
var pubCmd = &cobra.Command{
	Use:   "pub [flags]",
	Short: "Publish over skywire network",
	Long:  "Publish over skywire network\nPublish a local port over the skywire network. This will allow other nodes to access the local port via the skywire network.",
	Args:  cobra.MinimumNArgs(0),
	Run: func(cmd *cobra.Command, _ []string) {

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		if skyPort == 0 {
			skyPort = localPort
		}

		if localPort == 0 && skyPort == 0 {
			cmd.Help() //nolint
			os.Exit(0)
		}

		//port 0 is reserved / not usable
		if localPort == 0 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("port cannot be 0"))
		}

		//skyPort 0 is reserved / not usable
		if skyPort == 0 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("skyPort cannot be 0"))
		}

		//65535 is the highest TCP port number
		if 65536 < localPort {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("port cannot be greater than 65535"))
		}

		var appType appnet.AppType

		switch netType {
		case "http":
			appType = appnet.HTTP
		case "tcp":
			appType = appnet.TCP
		case "udp":
			appType = appnet.UDP
		default:
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid type"))
		}

		internal.Catch(cmd.Flags(), err)
		pubInfo, err := rpcClient.Publish(localPort, skyPort, appType)
		internal.Catch(cmd.Flags(), err)

		internal.PrintOutput(cmd.Flags(), pubInfo, fmt.Sprintf("Published on %s with ID: %s\n", pubInfo.SkyAddr.String(), pubInfo.ID.String()))

	},
}

// lsPubCmd lists all the publised apps on the skywire network by the visor
var lsPubCmd = &cobra.Command{
	Use:   "ls",
	Short: "List published apps on the skywire network by the visor",
	Long:  "List published apps on the skywire network by the visor\nThe list contains the id and the local port of the published app.",
	Args:  cobra.MinimumNArgs(0),
	Run: func(cmd *cobra.Command, args []string) {

		if len(args) != 0 {
			cmd.Help() //nolint
			os.Exit(0)
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		liss, err := rpcClient.ListPublished()
		internal.Catch(cmd.Flags(), err)
		var b bytes.Buffer
		w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', tabwriter.TabIndent)
		_, err = fmt.Fprintln(w, "id\tsky_port\tlocal_port\tapp_type")
		internal.Catch(cmd.Flags(), err)
		for _, lis := range liss {
			_, err = fmt.Fprintf(w, "%v\t%v\t%v\t%v\n", lis.ID, lis.SkyAddr.GetPort(), lis.LocalPort, lis.AppType)
			internal.Catch(cmd.Flags(), err)
		}
		internal.Catch(cmd.Flags(), w.Flush())
		internal.PrintOutput(cmd.Flags(), liss, b.String())
	},
}

// stopPubCmd stops a published app on the skywire network published by the visor
var stopPubCmd = &cobra.Command{
	Use:   "stop <id>",
	Short: "Stop a published app on the skywire network by the visor",
	Long:  "Stop a published app on the skywire network by the visor.\nThis will stop the published app and remove it from the skywire network.",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {

		if len(args) == 0 {
			cmd.Help() //nolint
			os.Exit(0)
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		id, err := uuid.Parse(args[0])
		internal.Catch(cmd.Flags(), err)
		err = rpcClient.Depublish(id)
		internal.Catch(cmd.Flags(), err)
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
	},
}
