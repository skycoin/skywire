// Package clidmsg cmd/skywire-cli/commands/dmsg/pty.go
package clidmsg

import (
	"context"
	"fmt"
	"log"
	"net"
	"strconv"
	"time"

	"github.com/skycoin/dmsg/pkg/dmsgpty"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/toqueteos/webbrowser"

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var (
	ptyPort    string
	ptyRpcAddr string
	ptyPath    string
	ptyPK      string
	ptyURL     string
	ptyPkg     bool
	ptyLogger  = logging.MustGetLogger("dmsgpty")
)

func init() {
	// pty command
	ptyCmd.AddCommand(
		ptyListCmd,
		ptyStartCmd,
		ptyUICmd,
		ptyURLCmd,
	)

	// Flags for list command
	ptyListCmd.PersistentFlags().StringVarP(&ptyRpcAddr, "rpc", "", "localhost:3435", "RPC server address")

	// Flags for start command
	ptyStartCmd.PersistentFlags().StringVarP(&ptyRpcAddr, "rpc", "", "localhost:3435", "RPC server address")
	ptyStartCmd.PersistentFlags().StringVarP(&ptyPort, "port", "p", "22", "port of remote visor dmsgpty")

	// Flags for ui command
	ptyUICmd.Flags().StringVarP(&ptyPath, "input", "i", "", "read from specified config file")
	ptyUICmd.Flags().BoolVarP(&ptyPkg, "pkg", "p", false, "read from "+visorconfig.SkywireConfig())
	ptyUICmd.Flags().StringVarP(&ptyPK, "visor", "v", "", "public key of visor to connect to")

	// Flags for url command
	ptyURLCmd.Flags().StringVarP(&ptyPath, "input", "i", "", "read from specified config file")
	ptyURLCmd.Flags().BoolVarP(&ptyPkg, "pkg", "p", false, "read from "+visorconfig.SkywireConfig())
	ptyURLCmd.Flags().StringVarP(&ptyPK, "visor", "v", "", "public key of visor to connect to")
}

// ptyCmd is the command for dmsgpty functionality
var ptyCmd = &cobra.Command{
	Use:   "pty",
	Short: "Interact with remote visors",
	Long:  "Commands for interacting with remote visors via dmsgpty",
}

var ptyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List connected visors",
	Run: func(cmd *cobra.Command, _ []string) {
		remoteVisors, err := ptyRPCClient(cmd.Flags()).RemoteVisors()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("RPC connection failed; is skywire running?: %v", err))
		}

		var msg string
		for idx, pk := range remoteVisors {
			msg += fmt.Sprintf("%d. %s\n", idx+1, pk)
		}
		internal.PrintOutput(cmd.Flags(), remoteVisors, msg)
	},
}

var ptyStartCmd = &cobra.Command{
	Use:   "start <pk>",
	Short: "Start dmsgpty session",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cli := dmsgpty.DefaultCLI()
		addr := internal.ParsePK(cmd.Flags(), "pk", args[0])
		port, _ := strconv.ParseUint(ptyPort, 10, 16) //nolint:errcheck
		ctx, cancel := cmdutil.SignalContext(context.Background(), nil)
		defer cancel()
		return cli.StartRemotePty(ctx, addr, uint16(port), dmsgpty.DefaultCmd)
	},
}

var ptyUICmd = &cobra.Command{
	Use:   "ui",
	Short: "Open dmsgpty UI in default browser",
	Run: func(cmd *cobra.Command, _ []string) {
		if ptyPK == "" {
			if ptyPkg {
				ptyPath = visorconfig.SkywireConfig()
			}
			if ptyPath != "" {
				conf, err := visorconfig.ReadFile(ptyPath)
				if err != nil {
					log.Fatal("Failed to read in config file:", err)
				}
				ptyURL = fmt.Sprintf("http://127.0.0.1:8000/pty/%s", conf.PK.Hex())
			} else {
				client := ptyRPCClient(cmd.Flags())
				overview, err := client.Overview()
				if err != nil {
					log.Fatal("Failed to connect; is skywire running?\n", err)
				}
				ptyURL = fmt.Sprintf("http://127.0.0.1:8000/pty/%s", overview.PubKey.Hex())
			}
		} else {
			ptyURL = fmt.Sprintf("http://127.0.0.1:8000/pty/%s", ptyPK)
		}
		if err := webbrowser.Open(ptyURL); err != nil {
			log.Fatal("Failed to open dmsgpty UI in browser:", err)
		}
	},
}

var ptyURLCmd = &cobra.Command{
	Use:   "url",
	Short: "Show dmsgpty UI URL",
	Run: func(cmd *cobra.Command, _ []string) {
		if ptyPK == "" {
			if ptyPkg {
				ptyPath = visorconfig.SkywireConfig()
			}
			if ptyPath != "" {
				conf, err := visorconfig.ReadFile(ptyPath)
				if err != nil {
					internal.Catch(cmd.Flags(), fmt.Errorf("Failed to read in config file: %v", err))
				}
				ptyURL = fmt.Sprintf("http://127.0.0.1:8000/pty/%s", conf.PK.Hex())
			} else {
				client := ptyRPCClient(cmd.Flags())
				overview, err := client.Overview()
				if err != nil {
					internal.Catch(cmd.Flags(), fmt.Errorf("Failed to connect; is skywire running?: %v", err))
				}
				ptyURL = fmt.Sprintf("http://127.0.0.1:8000/pty/%s", overview.PubKey.Hex())
			}
		} else {
			ptyURL = fmt.Sprintf("http://127.0.0.1:8000/pty/%s", ptyPK)
		}

		output := struct {
			URL string `json:"url"`
		}{
			URL: ptyURL,
		}

		internal.PrintOutput(cmd.Flags(), output, fmt.Sprintln(ptyURL))
	},
}

func ptyRPCClient(cmdFlags *pflag.FlagSet) visor.API {
	const rpcDialTimeout = time.Second * 5
	conn, err := net.DialTimeout("tcp", ptyRpcAddr, rpcDialTimeout)
	if err != nil {
		internal.PrintFatalError(cmdFlags, fmt.Errorf("RPC connection failed; is skywire running?: %v", err))
	}
	return visor.NewRPCClient(ptyLogger, conn, visor.RPCPrefix, 0)
}
