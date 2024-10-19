// Package commands cmd/dmsgpty-cli/commands/whitelist.go
package commands

import (
	"fmt"
	"log"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/spf13/cobra"
)

func init() {
	RootCmd.AddCommand(
		whitelistCmd,
		whitelistAddCmd,
		whitelistRemoveCmd)
}

var whitelistCmd = &cobra.Command{
	Use:   "whitelist",
	Short: "lists all whitelisted public keys",
	RunE: func(_ *cobra.Command, _ []string) error {
		wlC, err := cli.WhitelistClient()
		if err != nil {
			return err
		}
		pks, err := wlC.ViewWhitelist()
		if err != nil {
			return err
		}
		if len(pks) == 0 {
			log.Println("Whitelist Empty")
		} else {
			for _, pk := range pks {
				fmt.Println(pk)
			}
		}
		return nil
	},
}

var whitelistAddCmd = &cobra.Command{
	Use:   "whitelist-add <public-key>...",
	Short: "adds public key(s) to the whitelist",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {

		pks, err := pksFromArgs(args)
		if err != nil {
			return err
		}

		wlC, err := cli.WhitelistClient()
		if err != nil {
			return err
		}
		err = wlC.WhitelistAdd(pks...)
		if err != nil {
			fmt.Println(err)
			return nil
		}
		return nil
	},
}

var whitelistRemoveCmd = &cobra.Command{
	Use:   "whitelist-remove <public-key>...",
	Short: "removes public key(s) from the whitelist",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {

		pks, err := pksFromArgs(args)
		if err != nil {
			return err
		}

		wlC, err := cli.WhitelistClient()
		if err != nil {
			return err
		}
		return wlC.WhitelistRemove(pks...)
	},
}

func pksFromArgs(args []string) ([]cipher.PubKey, error) {
	pks := make([]cipher.PubKey, len(args))
	for i, str := range args {
		if err := pks[i].Set(str); err != nil {
			return nil, fmt.Errorf("failed to parse public key at index %d: %v", i, err)
		}
	}
	return pks, nil
}
