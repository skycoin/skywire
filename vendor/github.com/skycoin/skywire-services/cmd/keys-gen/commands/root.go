// Package commands cmd/keys-gen/commands/root.go
package commands

import (
	"fmt"
	"log"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/spf13/cobra"
)

// RootCmd contains the root command
var RootCmd = &cobra.Command{
	Use:   "kg",
	Short: "skywire keys generator, prints pub-key and sec-key",
	Long: `
	┬┌─┌─┐┬ ┬┌─┐   ┌─┐┌─┐┌┐┌
	├┴┐├┤ └┬┘└─┐───│ ┬├┤ │││
	┴ ┴└─┘ ┴ └─┘   └─┘└─┘┘└┘`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {
		pk, sk := cipher.GenerateKeyPair()
		fmt.Println(pk)
		fmt.Println(sk)
	},
}

// Execute executes root CLI command
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
