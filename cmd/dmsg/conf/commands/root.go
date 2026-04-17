// Package commands cmd/conf/commands/root.go
package commands

import (
	"fmt"
	"log"

	"github.com/bitfield/script"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

// RootCmd is the root command
var RootCmd = &cobra.Command{
	Short:                 `dmsg deployment servers config`,
	Long:                  `print the dmsg servers from the dmsghttp-config`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(_ *cobra.Command, _ []string) {
		_, err := script.Echo(string(dmsg.DmsghttpJSON)).JQ(`.prod.dmsg_servers`).Stdout()
		if err != nil {
			log.Fatal("Failed to execute command: ", err)
		}
	},
}

func init() {
	RootCmd.AddCommand(genKeysCmd, verifyKeysCmd)
}

var genKeysCmd = &cobra.Command{
	Use:   "gen-keys",
	Short: "Generate a new dmsg keypair",
	Run: func(_ *cobra.Command, _ []string) {
		pk, sk := cipher.GenerateKeyPair()
		fmt.Printf("%s\n%s\n", pk.Hex(), sk.Hex())
	},
}

var verifyKeysCmd = &cobra.Command{
	Use:   "verify-keys [secret-key]",
	Short: "Derive and print the public key from a secret key",
	Long:  "Derives the public key from the given secret key. Use to verify PK/SK pairs in config files.",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		var sk cipher.SecKey
		if err := sk.Set(args[0]); err != nil {
			return fmt.Errorf("invalid secret key: %w", err)
		}
		pk, err := sk.PubKey()
		if err != nil {
			return fmt.Errorf("failed to derive public key: %w", err)
		}
		fmt.Println(pk.Hex())
		return nil
	},
}

// Execute executes root CLI command.
func Execute() {
	dmsgclient.Execute(RootCmd)
}
