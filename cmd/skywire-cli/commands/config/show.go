// Package cliconfig cmd/skywire-cli/commands/config/show.go
package cliconfig

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var showPath bool

func init() {
	showCmd.Flags().BoolVar(&showPath, "path", false, "print only the config file path")
	showCmd.Flags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr, "RPC server address (env: SKYWIRE_RPC)")
	RootCmd.AddCommand(showCmd)
}

var showCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the running visor's config",
	Long: `Print the configuration currently in use by the running visor.
Use --path to print just the config file path.
Use --json for machine-readable output.`,
	Run: func(cmd *cobra.Command, _ []string) {
		if showPath {
			// Try RPC first to get the actual path the visor is using
			rpcClient, err := clirpc.Client(cmd.Flags())
			if err != nil {
				// Fallback: print the default config path
				fmt.Println(visorconfig.SkywireConfig())
				return
			}
			// GetRuntimeConfig returns the full config; we parse it for the path
			// But we don't actually have a path RPC. Use VisorConfigFile if available.
			configPath := visorconfig.VisorConfigFile
			if configPath == "" || configPath == visorconfig.Stdin {
				configPath = visorconfig.SkywireConfig()
			}
			_ = rpcClient // used to verify visor is running
			fmt.Println(configPath)
			return
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			// Fallback: try to read the config file directly
			configPath := visorconfig.SkywireConfig()
			data, readErr := os.ReadFile(configPath) //nolint:gosec
			if readErr != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("visor not running and config not found at %s: %v", configPath, readErr))
			}
			// Pretty-print
			var v interface{}
			if err := json.Unmarshal(data, &v); err == nil {
				pretty, _ := json.MarshalIndent(v, "", "  ") //nolint:errcheck
				internal.PrintOutput(cmd.Flags(), v, string(pretty)+"\n")
			} else {
				fmt.Println(string(data))
			}
			return
		}

		configJSON, err := rpcClient.GetRuntimeConfig()
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}

		var v interface{}
		if err := json.Unmarshal(configJSON, &v); err == nil {
			pretty, _ := json.MarshalIndent(v, "", "  ") //nolint:errcheck
			internal.PrintOutput(cmd.Flags(), v, string(pretty)+"\n")
		} else {
			fmt.Println(string(configJSON))
		}
	},
}
