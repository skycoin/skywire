// Package clitps cmd/skywire-cli/commands/tps/tps.go
package clitps

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

var (
	targetPK string
	remotePK string
	tpType   string
	tpID     string
)

func init() {
	RootCmd.Flags().SortFlags = false
	addCmd.Flags().SortFlags = false
	rmCmd.Flags().SortFlags = false
	listCmd.Flags().SortFlags = false

	RootCmd.AddCommand(
		addCmd,
		rmCmd,
		listCmd,
	)

	// Common flag
	RootCmd.PersistentFlags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr, "RPC server address (env: SKYWIRE_RPC)")

	// Add transport flags
	addCmd.Flags().StringVarP(&targetPK, "target", "t", "", "target visor public key (visor to add transport on)")
	addCmd.Flags().StringVarP(&remotePK, "remote", "r", "", "remote visor public key (other transport edge)")
	addCmd.Flags().StringVarP(&tpType, "type", "T", "stcpr", "transport type (stcpr, sudph, dmsg)")
	addCmd.MarkFlagRequired("target") //nolint:errcheck,gosec
	addCmd.MarkFlagRequired("remote") //nolint:errcheck,gosec

	// Remove transport flags
	rmCmd.Flags().StringVarP(&targetPK, "target", "t", "", "target visor public key")
	rmCmd.Flags().StringVarP(&tpID, "id", "i", "", "transport ID to remove")
	rmCmd.MarkFlagRequired("target") //nolint:errcheck,gosec
	rmCmd.MarkFlagRequired("id")     //nolint:errcheck,gosec

	// List transports flags
	listCmd.Flags().StringVarP(&targetPK, "target", "t", "", "target visor public key")
	listCmd.MarkFlagRequired("target") //nolint:errcheck,gosec
}

// RootCmd is the TPS command group
var RootCmd = &cobra.Command{
	Use:   "tps",
	Short: "Control embedded Transport Setup Node",
	Long: `Commands to control the embedded Transport Setup Node (TPS).

The embedded TPS allows this visor to manage transports on other visors
that trust this visor's TPS public key.

To enable embedded TPS, set 'tps_sk' in the visor config.`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("RPC connection failed: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck

		status, err := rpcClient.TPSStatus()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to get TPS status: %w", err))
		}

		if status.Enabled {
			fmt.Printf("TPS Status: enabled\n")
			fmt.Printf("TPS Public Key: %s\n", status.PubKey)
		} else {
			fmt.Printf("TPS Status: disabled\n")
			fmt.Printf("To enable, set 'tps_sk' in the visor config\n")
		}
	},
}

var addCmd = &cobra.Command{
	Use:   "add",
	Short: "Add transport on target visor",
	Long: `Request the target visor to create a transport to the remote visor.

The target visor must trust this visor's TPS public key.`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("RPC connection failed: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck

		var target cipher.PubKey
		if err := target.Set(targetPK); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid target public key: %w", err))
		}

		var remote cipher.PubKey
		if err := remote.Set(remotePK); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid remote public key: %w", err))
		}

		resp, err := rpcClient.TPSAddTransport(target, remote, tpType)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to add transport: %w", err))
		}

		fmt.Printf("Transport created successfully:\n")
		fmt.Printf("  ID:     %s\n", resp.ID)
		fmt.Printf("  Local:  %s\n", resp.Local)
		fmt.Printf("  Remote: %s\n", resp.Remote)
		fmt.Printf("  Type:   %s\n", resp.Type)
	},
}

var rmCmd = &cobra.Command{
	Use:   "rm",
	Short: "Remove transport on target visor",
	Long:  `Request the target visor to remove a transport by ID.`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("RPC connection failed: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck

		var target cipher.PubKey
		if err := target.Set(targetPK); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid target public key: %w", err))
		}

		id, err := uuid.Parse(tpID)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid transport ID: %w", err))
		}

		if err := rpcClient.TPSRemoveTransport(target, id); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to remove transport: %w", err))
		}

		fmt.Printf("Transport %s removed successfully\n", id)
	},
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List transports on target visor",
	Long:  `Get the list of transports from a target visor.`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("RPC connection failed: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck

		var target cipher.PubKey
		if err := target.Set(targetPK); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid target public key: %w", err))
		}

		transports, err := rpcClient.TPSGetTransports(target)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to get transports: %w", err))
		}

		if len(transports) == 0 {
			fmt.Printf("No transports found on %s\n", target)
			return
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', tabwriter.TabIndent)
		fmt.Fprintln(w, "ID\tLOCAL\tREMOTE\tTYPE") //nolint:errcheck
		for _, tp := range transports {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", tp.ID, tp.Local, tp.Remote, tp.Type) //nolint:errcheck
		}
		w.Flush() //nolint:errcheck,gosec
	},
}
