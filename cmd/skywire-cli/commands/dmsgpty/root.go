package dmsgpty

import (
	"fmt"
	"net"
	"os"
	"time"

	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/visor"
)

var rpcAddr string
var masterLogger = logging.NewMasterLogger()
var packageLogger = masterLogger.PackageLogger("dmsgpty")

func init() {
	RootCmd.PersistentFlags().StringVarP(&rpcAddr, "rpc", "", "localhost:3435", "RPC server address")
}

// RootCmd is the command that contains sub-commands which interacts with dmsgpty.
var RootCmd = &cobra.Command{
	Use:   "dmsgpty",
	Short: "Some simple commands of dmsgpty to remote visor",
}

func init() {
	RootCmd.AddCommand(
		listOfVisors,
		executeCommand,
	)
}

var listOfVisors = &cobra.Command{
	Use:   "list",
	Short: "fetch list of connected visor's PKs",
	Run: func(_ *cobra.Command, _ []string) {
		remoteVisors := rpcClient().RemoteVisors()
		var msg string
		for idx, pk := range remoteVisors {
			msg += fmt.Sprintf("%d. %s\n", idx+1, pk.String())
		}
		if _, err := os.Stdout.Write([]byte(msg)); err != nil {
			packageLogger.Fatal("Failed to output build info:", err)
		}
	},
}

var executeCommand = &cobra.Command{
	Use:   "exec <visor-public-key> <command>",
	Short: "fetch available servers",
	Args:  cobra.MinimumNArgs(2),
	Run: func(_ *cobra.Command, args []string) {
		var msg []byte
		pk := internal.ParsePK("visor-public-key", args[0])
		msg, err := rpcClient().DmsgPtyExec(pk, args[1])
		if err != nil {
			msg = []byte(fmt.Sprintf("%s\n", err.Error()))
		}
		if _, err := os.Stdout.Write(msg); err != nil {
			packageLogger.Fatal("Failed to output build info:", err)
		}
	},
}

func rpcClient() visor.API {
	const rpcDialTimeout = time.Second * 5
	conn, err := net.DialTimeout("tcp", rpcAddr, rpcDialTimeout)
	if err != nil {
		packageLogger.Fatal("RPC connection failed:", err)
	}
	return visor.NewRPCClient(packageLogger, conn, visor.RPCPrefix, 0)
}
