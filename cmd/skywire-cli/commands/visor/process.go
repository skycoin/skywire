// Package clivisor cmd/skywire-cli/commands/visor/process.go
package clivisor

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"time"

	"github.com/bitfield/script"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
)

var sourcerun bool
var root bool

func init() {
	usrLvl, err := user.Current()
	if err != nil {
		panic(err)
	}
	if usrLvl.Username == "root" {
		root = true
	}
	RootCmd.AddCommand(startCmd)
	RootCmd.AddCommand(reloadCmd)
	RootCmd.AddCommand(shutdownCmd)
	startCmd.Flags().BoolVarP(&sourcerun, "src", "s", false, "'go run' external commands from the skywire sources")
}

// TODO(ersonp): get help from moses for it's usage
var startCmd = &cobra.Command{
	Use:   "start",
	Short: "start visor",
	Run: func(cmd *cobra.Command, _ []string) {
		var output string
		var err error
		if !sourcerun {
			if root {
				//if skywire is installed as a command from a package, we can use the -p flag here
				output, err = script.Exec(`skywire-visor -p`).String()
			} else {
				//if the config exists in the userspace and this command was not run as root
				output, err = script.Exec(`skywire-visor -u`).String()
			}
		} else {
			output, err = script.Exec(`bash -c 'go run cmd/skywire-visor/skywire-visor.go'`).String()
		}
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to start visor: %v", err))
		}
		internal.PrintOutput(cmd.Flags(), output, fmt.Sprintln(output))
	},
}

func gort(ctx context.Context, fn func() error) error {
	errs, _ := errgroup.WithContext(ctx)
	errs.Go(func() error {
		return fn()
	})
	// Wait for completion and return the first error (if any)
	return errs.Wait()
}

var reloadCmd = &cobra.Command{
	Use:    "reload",
	Short:  "reload visor",
	Hidden: true,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		go func() {
			err = gort(context.Background(), rpcClient.Reload)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("error reloading visor"))
			}
		}()

		<-time.After(1 * time.Second)
		internal.PrintOutput(cmd.Flags(), "Visor reloaded", fmt.Sprintln("Visor reloaded"))

	},
}

func init() {
}

var shutdownCmd = &cobra.Command{
	Use:   "halt",
	Short: "Stop a running visor",
	Long:  "\n  Stop a running visor",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		rpcClient.Shutdown() //nolint
		internal.PrintOutput(cmd.Flags(), "Visor was shut down", fmt.Sprintln("Visor was shut down"))
	},
}
