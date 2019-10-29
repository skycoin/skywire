package commands

import (
	"errors"
	"fmt"
	"math/big"
	"sort"


	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/spf13/cobra"

	"github.com/SkycoinProject/skywire-mainnet/pkg/dmsgpty/ptycfg"
)

func init() {
	rootCmd.AddCommand(
		whitelistCmd,
		whitelistAddCmd,
		whitelistRemoveCmd)
}

var whitelistCmd = &cobra.Command{
	Use:   "whitelist",
	Short: "lists all whitelisted public keys",
	RunE: func(_ *cobra.Command, _ []string) error {
		conn, err := ptyCLI.RequestCfg()
		if err != nil {
			return err
		}
		pks, err := ptycfg.ViewWhitelist(conn)
		if err != nil {
			return err
		}
		sort.Slice(pks, func(i, j int) bool {
			var a, b big.Int
			a.SetBytes(pks[i][:])
			b.SetBytes(pks[j][:])
			return a.Cmp(&b) >= 0
		})
		for _, pk := range pks {
			fmt.Println(pk)
		}
		return nil
	},
}

var pk cipher.PubKey

func init() {
	whitelistAddCmd.Flags().Var(&pk, "pk", "public key of remote")
}

var whitelistAddCmd = &cobra.Command{
	Use:   "whitelist-add",
	Short: "adds a public key to whitelist",
	PreRunE: func(*cobra.Command, []string) error {
		if pk.Null() {
			return errors.New("cannot add a null public key to the whitelist")
		}
		return nil
	},
	RunE: func(_ *cobra.Command, _ []string) error {
		conn, err := ptyCLI.RequestCfg()
		if err != nil {
			return err
		}
		if err := ptycfg.WhitelistAdd(conn, pk); err != nil {
			return fmt.Errorf("failed to add public key '%s' to the whitelist: %v", pk, err)
		}
		fmt.Println("OK")
		return nil
	},
}

func init() {
	whitelistRemoveCmd.Flags().Var(&pk, "pk", "public key of remote")
}

var whitelistRemoveCmd = &cobra.Command{
	Use:   "whitelist-remove",
	Short: "removes a public key from the whitelist",
	PreRunE: func(*cobra.Command, []string) error {
		if pk.Null() {
			return errors.New("cannot add a null public key to the whitelist")
		}
		return nil
	},
	RunE: func(_ *cobra.Command, _ []string) error {
		conn, err := ptyCLI.RequestCfg()
		if err != nil {
			return err
		}
		if err := ptycfg.WhitelistRemove(conn, pk); err != nil {
			return fmt.Errorf("failed to add public key '%s' to the whitelist: %v", pk, err)
		}
		fmt.Println("OK")
		return nil
	},
}
