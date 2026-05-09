// Package run cmd/svc/skywire-services/commands/run/run.go
//
// Implements `skywire svc run --config services.json` — the
// multi-service-in-one-process supervisor. Each service block in the
// JSON file maps to a Factory previously installed by per-service
// package init() (see pkg/services/dmsgdisc, pkg/services/tpd, ...).
//
// This subcommand is the ONLY entry point that runs more than one
// service in a single process. The existing per-service subcommands
// (`skywire svc tpd`, `skywire dmsg dmsg-discovery start`, ...)
// continue to work unchanged for production deployments — they each
// build their own pkg/services.Service and run it directly without
// going through the registry.
package run

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services"
)

var (
	configPath string
	listOnly   bool
)

func init() {
	RootCmd.Flags().StringVarP(&configPath, "config", "c", "",
		"path to services.json (required unless --list)")
	RootCmd.Flags().BoolVar(&listOnly, "list", false,
		"print the registered service types and exit")
}

// RootCmd implements `skywire svc run`.
var RootCmd = &cobra.Command{
	Use:   "run",
	Short: "Run multiple deployment services in one process",
	Long: `Run multiple deployment services in one process.

Reads a JSON config file describing which services to host and starts
each one. Used to collapse the per-service docker containers in CI
e2e and small all-in-one deployments down to a single binary.

Example services.json:

  {
    "services": [
      { "type": "dmsg-discovery",     "addr": ":9090", ... },
      { "type": "dmsg-server",        ... },
      { "type": "transport-discovery", ... }
    ]
  }

Use --list to see which service types are registered.`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	RunE: func(_ *cobra.Command, _ []string) error {
		if listOnly {
			types := services.RegisteredTypes()
			if len(types) == 0 {
				fmt.Fprintln(os.Stdout, "no service types registered") //nolint:errcheck
				return nil
			}
			fmt.Fprintln(os.Stdout, "registered service types:") //nolint:errcheck
			for _, t := range types {
				fmt.Fprintf(os.Stdout, "  %s\n", t) //nolint:errcheck
			}
			return nil
		}
		if configPath == "" {
			return fmt.Errorf("--config is required (or pass --list)")
		}
		if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "failed to print build info: %v\n", err)
		}
		master := logging.NewMasterLogger()
		log := master.PackageLogger("skywire-svc-run")

		file, err := services.LoadFile(configPath)
		if err != nil {
			return err
		}
		log.WithField("count", len(file.Services)).
			WithField("config", configPath).
			Info("loaded services config")

		// Sanity-check every block's type up front so the error
		// message points at the bad block rather than going silent
		// until the supervisor tries to build it.
		for i, b := range file.Services {
			if _, ok := services.Lookup(b.Type); !ok {
				return fmt.Errorf("services.json[%d]: unknown type %q (registered: %v)",
					i, b.Type, services.RegisteredTypes())
			}
		}

		return services.Run(context.Background(), file, master)
	},
}
