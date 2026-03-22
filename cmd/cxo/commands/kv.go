package commands

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/spf13/cobra"
)

var kvCmd = &cobra.Command{
	Use:   "kv",
	Short: "Key-value store commands",
}

func init() {
	kvCmd.AddCommand(kvCreateCmd, kvPutCmd, kvGetCmd, kvDeleteCmd, kvListCmd)
}

var kvCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new KV store (returns feed pubkey and secret key)",
	RunE: func(cmd *cobra.Command, args []string) error {
		r, err := getCLIRPCClient()
		if err != nil {
			return err
		}
		defer r.Close() //nolint:errcheck

		result, err := r.KV().Create()
		if err != nil {
			return err
		}

		fmt.Fprintf(out, "Feed:   %s\n", result.Feed.Hex())                              //nolint:errcheck
		fmt.Fprintf(out, "Secret: %s\n", hex.EncodeToString(result.Secret[:]))           //nolint:errcheck
		fmt.Fprintln(out, "\nSave the secret key — you need it to write to this store.") //nolint:errcheck
		return nil
	},
}

var kvPutCmd = &cobra.Command{
	Use:   "put <feed> <secret> <key> <value>",
	Short: "Set a key-value pair",
	Args:  cobra.ExactArgs(4),
	RunE: func(cmd *cobra.Command, args []string) error {
		feed, err := parsePubKey(args[0])
		if err != nil {
			return fmt.Errorf("invalid feed pubkey: %w", err)
		}
		sk, err := parseSecKey(args[1])
		if err != nil {
			return fmt.Errorf("invalid secret key: %w", err)
		}

		r, err := getCLIRPCClient()
		if err != nil {
			return err
		}
		defer r.Close() //nolint:errcheck

		if err := r.KV().Put(feed, sk, args[2], args[3]); err != nil {
			return err
		}

		fmt.Fprintln(out, "OK") //nolint:errcheck
		return nil
	},
}

var kvGetCmd = &cobra.Command{
	Use:   "get <feed> <key>",
	Short: "Get a value by key",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		feed, err := parsePubKey(args[0])
		if err != nil {
			return fmt.Errorf("invalid feed pubkey: %w", err)
		}

		r, err := getCLIRPCClient()
		if err != nil {
			return err
		}
		defer r.Close() //nolint:errcheck

		value, found, err := r.KV().Get(feed, args[1])
		if err != nil {
			return err
		}

		if !found {
			return fmt.Errorf("key %q not found", args[1])
		}

		fmt.Fprintln(out, value) //nolint:errcheck
		return nil
	},
}

var kvDeleteCmd = &cobra.Command{
	Use:   "delete <feed> <secret> <key>",
	Short: "Delete a key",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		feed, err := parsePubKey(args[0])
		if err != nil {
			return fmt.Errorf("invalid feed pubkey: %w", err)
		}
		sk, err := parseSecKey(args[1])
		if err != nil {
			return fmt.Errorf("invalid secret key: %w", err)
		}

		r, err := getCLIRPCClient()
		if err != nil {
			return err
		}
		defer r.Close() //nolint:errcheck

		if err := r.KV().Delete(feed, sk, args[2]); err != nil {
			return err
		}

		fmt.Fprintln(out, "OK") //nolint:errcheck
		return nil
	},
}

var kvListCmd = &cobra.Command{
	Use:   "list <feed>",
	Short: "List all key-value pairs in a store",
	Long:  "List all key-value pairs. Use --json for machine-readable output.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		feed, err := parsePubKey(args[0])
		if err != nil {
			return fmt.Errorf("invalid feed pubkey: %w", err)
		}

		r, err := getCLIRPCClient()
		if err != nil {
			return err
		}
		defer r.Close() //nolint:errcheck

		entries, err := r.KV().List(feed)
		if err != nil {
			return err
		}

		jsonFlag, _ := cmd.Flags().GetBool("json") //nolint:errcheck
		if jsonFlag {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(entries)
		}

		if len(entries) == 0 {
			fmt.Fprintln(out, "(empty)") //nolint:errcheck
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "KEY\tVALUE") //nolint:errcheck
		for _, e := range entries {
			fmt.Fprintf(w, "%s\t%s\n", e.Key, e.Value) //nolint:errcheck
		}
		return w.Flush()
	},
}

func init() {
	kvListCmd.Flags().Bool("json", false, "output as JSON")
}

func parsePubKey(s string) (cipher.PubKey, error) {
	var pk cipher.PubKey
	b, err := hex.DecodeString(s)
	if err != nil {
		return pk, err
	}
	if len(b) != len(pk) {
		return pk, fmt.Errorf("invalid pubkey length: %d", len(b))
	}
	copy(pk[:], b)
	return pk, nil
}

func parseSecKey(s string) (cipher.SecKey, error) {
	var sk cipher.SecKey
	b, err := hex.DecodeString(s)
	if err != nil {
		return sk, err
	}
	if len(b) != len(sk) {
		return sk, fmt.Errorf("invalid secret key length: %d", len(b))
	}
	copy(sk[:], b)
	return sk, nil
}
