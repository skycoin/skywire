// Package commands cmd/version/commands/root.go
package commands

import (
	"fmt"
	"log"
	"github.com/spf13/cobra"
	"runtime/debug"
	"path/filepath"
	"os"
	"strings"
)


var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: `version`,
	Long: `version`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(_ *cobra.Command, _ []string) {
		bi, ok := debug.ReadBuildInfo()
		if !ok {
			panic("couldn't read build info")
		}

		fmt.Printf("%s version %s\n", bi.Path, bi.Main.Version)
	},
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
