package clidmsgpty

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/skycoin/dmsg/pkg/dmsgpty"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	ptyPort       string
	masterLogger  = logging.NewMasterLogger()
	packageLogger = masterLogger.PackageLogger("dmsgpty")
	rpcAddr       string
	path          string
	pk            string
	url           string
	pkg           bool
)

func init() {
	visorsCmd.PersistentFlags().StringVarP(&rpcAddr, "rpc", "", "localhost:3435", "RPC server address")
	shellCmd.PersistentFlags().StringVarP(&rpcAddr, "rpc", "", "localhost:3435", "RPC server address")
	shellCmd.PersistentFlags().StringVarP(&ptyPort, "port", "p", "22", "port of remote visor dmsgpty")
}

// RootCmd is the command that contains sub-commands which interacts with dmsgpty.
var RootCmd = &cobra.Command{
	Use:   "dmsgpty",
	Short: "Interact with remote visors",
}

func init() {
	RootCmd.AddCommand(
		visorsCmd,
		shellCmd,
	)
}

var visorsCmd = &cobra.Command{
	Use:   "list",
	Short: "List connected visors",
	Run: func(cmd *cobra.Command, _ []string) {
		remoteVisors, err := rpcClient().RemoteVisors()
		if err != nil {
			internal.PrintError(cmd.Flags(), fmt.Errorf("RPC connection failed; is skywire running?: %v", err))
		}

		var msg string
		for idx, pk := range remoteVisors {
			msg += fmt.Sprintf("%d. %s\n", idx+1, pk)
		}
		internal.PrintOutput(cmd.Flags(), remoteVisors, msg)
	},
}

var shellCmd = &cobra.Command{
	Use:   "start <pk>",
	Short: "Start dmsgpty session",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cli := dmsgpty.DefaultCLI()
		addr := internal.ParsePK(cmd.Flags(), "pk", args[0])
		port, _ := strconv.ParseUint(ptyPort, 10, 16) //nolint
		ctx, cancel := cmdutil.SignalContext(context.Background(), nil)
		defer cancel()
		return cli.StartRemotePty(ctx, addr, uint16(port), dmsgpty.DefaultCmd)
	},
}

func rpcClient() visor.API {
	const rpcDialTimeout = time.Second * 5
	conn, err := net.DialTimeout("tcp", rpcAddr, rpcDialTimeout)
	if err != nil {
		packageLogger.Fatal("RPC connection failed; is skywire running?\n", err)
	}
	return visor.NewRPCClient(packageLogger, conn, visor.RPCPrefix, 0)
}
