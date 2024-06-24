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
	depublish string
	netType   string
	skyPort   int
)

func init() {
	pubCmd.PersistentFlags().IntVarP(&localPort, "port", "p", 0, "local port of the external (http, tcp, udp) app")
	pubCmd.PersistentFlags().IntVarP(&skyPort, "skyport", "s", localPort, "skywire port for the external (http, tcp, udp) app")
	pubCmd.PersistentFlags().StringVarP(&depublish, "depublish", "d", "", "deregister local port of the external (http, tcp, udp) app with id")
	pubCmd.PersistentFlags().StringVarP(&netType, "type", "t", "http", "type of the external app connection (http, tcp, udp)")
	pubCmd.PersistentFlags().BoolVarP(&lsPorts, "ls", "l", false, "list published local ports")
	RootCmd.AddCommand(pubCmd)
}

// pubCmd contains commands to publish over the skywire network
var pubCmd = &cobra.Command{
	Use:   "pub",
	Short: "Publish over skywire network",
	Long:  "Publish over skywire network\nPublish a local port over the skywire network. This will allow other nodes to access the local port via the skywire network.",
	Args:  cobra.MinimumNArgs(0),
	Run: func(cmd *cobra.Command, _ []string) {

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		if depublish != "" {
			id, err := uuid.Parse(depublish)
			internal.Catch(cmd.Flags(), err)
			err = rpcClient.Depublish(id)
			internal.Catch(cmd.Flags(), err)
			internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
			os.Exit(0)
		}

		if lsPorts {
			liss, err := rpcClient.ListPublished()
			internal.Catch(cmd.Flags(), err)
			var b bytes.Buffer
			w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', tabwriter.TabIndent)
			_, err = fmt.Fprintln(w, "id\tlocal_port")
			internal.Catch(cmd.Flags(), err)
			for id, lis := range liss {
				_, err = fmt.Fprintf(w, "%v\t%v\n", id, lis.LocalAddr.GetPort())
				internal.Catch(cmd.Flags(), err)
			}
			internal.Catch(cmd.Flags(), w.Flush())
			internal.PrintOutput(cmd.Flags(), liss, b.String())
			os.Exit(0)
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
		id, err := rpcClient.Publish(localPort, skyPort, appType)
		internal.Catch(cmd.Flags(), err)
		internal.PrintOutput(cmd.Flags(), "id: %v\n", fmt.Sprintln(id))

	},
}
