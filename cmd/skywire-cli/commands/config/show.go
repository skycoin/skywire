// Package cliconfig cmd/skywire-cli/commands/config/show.go
package cliconfig

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/itchyny/gojq"
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
	Use:   "show [jq-filter]",
	Short: "Show the running visor's config",
	Long: `Print the configuration currently in use by the running visor.

An optional jq filter can be provided to extract specific fields:

  skywire cli config show .dmsg
  skywire cli config show .transport.public_autoconnect
  skywire cli config show '.launcher.apps[] | .name'
  skywire cli config show .dht
  skywire cli config show .routing.route_setup_nodes

Use --path to print just the config file path.`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if showPath {
			// Ask the running visor for the path it actually loaded.
			// Falls back to the default install path only when no visor
			// is reachable (CLI invoked without a running visor).
			path := visorconfig.SkywireConfig()
			rpcClient, err := clirpc.Client(cmd.Flags())
			if err == nil {
				configPath, err := rpcClient.GetConfigPath()
				if err == nil && configPath != "" {
					path = configPath
				}
			}
			internal.PrintOutput(cmd.Flags(), path, path+"\n")
			return
		}

		// Get the config JSON — try RPC first, then file fallback.
		var configData []byte
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			configPath := visorconfig.SkywireConfig()
			data, readErr := os.ReadFile(configPath) //nolint:gosec
			if readErr != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("visor not running and config not found at %s: %v", configPath, readErr))
			}
			configData = data
		} else {
			configData, err = rpcClient.GetRuntimeConfig()
			if err != nil {
				internal.PrintFatalRPCError(cmd.Flags(), err)
			}
		}

		var v interface{}
		if err := json.Unmarshal(configData, &v); err != nil {
			internal.PrintOutput(cmd.Flags(), string(configData), string(configData)+"\n")
			return
		}

		// Apply jq filter if provided. Collect results so --json emits a
		// single array (when multiple) or the lone value, matching how
		// jq itself behaves with -c off.
		if len(args) > 0 && args[0] != "" {
			query, err := gojq.Parse(args[0])
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid filter %q: %w", args[0], err))
			}
			code, err := gojq.Compile(query)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("compile filter: %w", err))
			}
			var results []interface{}
			var text string
			iter := code.Run(v)
			for {
				result, ok := iter.Next()
				if !ok {
					break
				}
				if err, isErr := result.(error); isErr {
					internal.PrintFatalError(cmd.Flags(), err)
				}
				out, _ := json.MarshalIndent(result, "", "  ") //nolint:errcheck
				text += string(out) + "\n"
				results = append(results, result)
			}
			var payload interface{} = results
			if len(results) == 1 {
				payload = results[0]
			}
			internal.PrintOutput(cmd.Flags(), payload, text)
			return
		}

		pretty, _ := json.MarshalIndent(v, "", "  ") //nolint:errcheck
		internal.PrintOutput(cmd.Flags(), v, string(pretty)+"\n")
	},
}
