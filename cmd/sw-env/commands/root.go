// Package commands cmd/sw-env/commands/root.go
package commands

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	cfg "github.com/skycoin/skywire/internal/config"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
)

// RootCmd contains the root command
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short:                 "skywire environment generator",
	Long:                  calvin.AsciiFont("sw-env"),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {
		switch {
		case publicFlag:
			fmt.Println(cfg.PrintJSON(cfg.DefaultPublicEnv()))
		case localFlag:
			fmt.Println(cfg.PrintJSON(cfg.DefaultLocalEnv()))
		case dockerFlag:
			fmt.Println(cfg.PrintJSON(cfg.DefaultDockerizedEnv(dockerNetwork)))
		}
	},
}

var (
	publicFlag    bool
	localFlag     bool
	dockerFlag    bool
	dockerNetwork string
)

func init() {
	RootCmd.AddCommand(
		visorCmd,
		dmsgCmd,
		setupCmd,
	)
	RootCmd.Flags().BoolVarP(&publicFlag, "public", "p", false, "Environment with public skywire-services")
	RootCmd.Flags().BoolVarP(&localFlag, "local", "l", false, "Environment with skywire-services on localhost")
	RootCmd.Flags().BoolVarP(&dockerFlag, "docker", "d", false, "Environment with dockerized skywire-services")
	RootCmd.Flags().StringVarP(&dockerNetwork, "network", "n", "SKYNET", "Docker network to use")
}

var visorCmd = &cobra.Command{
	Use:   "visor",
	Short: "Generate config for skywire-visor",
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Println(cfg.PrintJSON(cfg.DefaultPublicVisorConfig()))
	},
}

var dmsgCmd = &cobra.Command{
	Use:   "dmsg",
	Short: "Generate config for dmsg-server",
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Println(cfg.PrintJSON(cfg.EmptyDmsgServerConfig()))
	},
}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Generate config for setup node",
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Println(cfg.PrintJSON(cfg.EmptySetupNodeConfig()))
	},
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
