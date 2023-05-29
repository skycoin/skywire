// Package clirpc root.go
package clirpc

import (
	"fmt"
	"net"
	"time"
	"reflect"
	"github.com/spf13/pflag"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	logger = logging.MustGetLogger("skywire-cli")
	//Addr is the address (ip:port) of the rpc server
	Addr string
)

// Client is used by other skywire-cli commands to query the visor rpc
func Client(cmdFlags *pflag.FlagSet) (visor.API, error) {
	const rpcDialTimeout = time.Second * 5
	conn, err := net.DialTimeout("tcp", Addr, rpcDialTimeout)
	if err != nil {
		internal.PrintError(cmdFlags, fmt.Errorf("RPC connection failed; is skywire running?: %v", err))
		return nil, err
	}
	return visor.NewRPCClient(logger, conn, visor.RPCPrefix, 0), nil
}


// CheckMethod checks for the existence of the RPC method before calling it.
func CheckMethod(rpcClient visor.API, rpcMethod string) error {
	// Get the type of the client object.
	clientType := reflect.TypeOf(rpcClient)

	// Check if the method exists in the rpc client type.
	for i := 0; i < clientType.NumMethod(); i++ {
		method := clientType.Method(i)
		if method.Name == rpcMethod {
			// Method found, return nil.
			return nil
		}
	}
	// Method not found, return error.
	return fmt.Errorf("RPC method not found: %s", rpcMethod)
}

var RootCmd = &cobra.Command{
	Use:                   "rpc",
	Short:                 "list available rpc methods",
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		// Get the type of the client object.
		clientType := reflect.TypeOf(rpcClient)

		// Get the exported methods of the client type.
		for i := 0; i < clientType.NumMethod(); i++ {
			method := clientType.Method(i)
			fmt.Println(method.Name)
		}
		// Close the connection.
		//rpcClient.Close()

		},
}
