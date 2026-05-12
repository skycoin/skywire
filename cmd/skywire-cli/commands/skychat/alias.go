// Package cliskychat cmd/skywire-cli/commands/skychat/alias.go
//
// Local PK addressbook for skychat. 66-hex PKs are brittle to type
// and to remember; this lets operators alias them by short name and
// use the alias anywhere a -t / --to / --peer / --from accepts a PK.
//
// Storage: $XDG_CONFIG_HOME/skywire/skychat-aliases.json (falls back
// to ~/.config/skywire/skychat-aliases.json via os.UserConfigDir).
// Flat JSON {name: pk_hex}. No locking — concurrent writes from two
// CLI invocations would race; in practice an interactive operator
// runs one command at a time. File is rewritten atomically (write to
// temp + rename) on every Set / Del.
//
// Resolution rule (resolveTarget): input is a PK if and only if it
// passes cipher.PubKey.Set. Anything else is treated as an alias
// lookup. If neither matches, the caller surfaces the error.
//
// Names: arbitrary string, but a leading "0" digit is reserved (so
// a typo on a real PK doesn't get treated as an alias). Empty names
// are rejected. Names are case-sensitive — "claude2" and "Claude2"
// are different aliases. The "Other Claude" use case wants this
// behavior because operators distinguish hosts by case-sensitive
// hostnames already.

package cliskychat

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/pkg/cipher"
)

const aliasFileName = "skywire/skychat-aliases.json"

// aliasStore is a tiny on-disk JSON map. Load returns an empty store
// (not an error) when the file doesn't exist — first-run new install.
type aliasStore struct {
	path    string
	entries map[string]string
}

func aliasPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config dir: %w", err)
	}
	return filepath.Join(dir, aliasFileName), nil
}

func loadAliases() (*aliasStore, error) {
	p, err := aliasPath()
	if err != nil {
		return nil, err
	}
	s := &aliasStore{path: p, entries: map[string]string{}}
	raw, err := os.ReadFile(p) //nolint:gosec
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, fmt.Errorf("read aliases: %w", err)
	}
	if len(raw) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(raw, &s.entries); err != nil {
		return nil, fmt.Errorf("parse aliases at %s: %w", p, err)
	}
	return s, nil
}

func (s *aliasStore) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("mkdir aliases dir: %w", err)
	}
	body, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	// Atomic write: temp in same dir + rename. Same-dir rename is
	// atomic on POSIX; cross-fs would not be.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return fmt.Errorf("write aliases tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp) //nolint:errcheck
		return fmt.Errorf("rename aliases: %w", err)
	}
	return nil
}

// lookupAlias returns the alias name registered for the given
// hex-encoded PK, or "" if none. Best-effort: errors loading the
// store return "" so display paths never fail on a missing/invalid
// aliases file.
func lookupAlias(pkHex string) string {
	if pkHex == "" {
		return ""
	}
	s, err := loadAliases()
	if err != nil {
		return ""
	}
	for name, hex := range s.entries {
		if hex == pkHex {
			return name
		}
	}
	return ""
}

// resolveTarget turns a -t / --to / --peer / --from string into a
// cipher.PubKey. Behavior:
//
//   - 66-hex PK accepted directly (and never looked up in the alias
//     store, even on accidental name collision).
//   - any other input is treated as an alias name; the store is
//     loaded and consulted.
//   - empty input → error.
//
// On unknown alias the error names the missing key so the operator
// can correct it without `alias ls` first.
func resolveTarget(input string) (cipher.PubKey, error) {
	if input == "" {
		return cipher.PubKey{}, errors.New("recipient is empty")
	}
	var pk cipher.PubKey
	if err := pk.Set(input); err == nil {
		return pk, nil
	}
	s, err := loadAliases()
	if err != nil {
		return cipher.PubKey{}, fmt.Errorf("looking up %q: %w", input, err)
	}
	hex, ok := s.entries[input]
	if !ok {
		return cipher.PubKey{}, fmt.Errorf("%q is neither a valid PK nor a known alias (try: skywire cli skychat alias ls)", input)
	}
	if err := pk.Set(hex); err != nil {
		return cipher.PubKey{}, fmt.Errorf("alias %q maps to invalid PK %q: %w", input, hex, err)
	}
	return pk, nil
}

var aliasCmd = &cobra.Command{
	Use:   "alias",
	Short: "Manage local PK aliases",
	Long: `Manage a local addressbook of name → public-key aliases.

Aliases are stored at $XDG_CONFIG_HOME/skywire/skychat-aliases.json
(falls back to ~/.config/skywire/skychat-aliases.json).

Use a name anywhere a -t / --to / --peer / --from accepts a PK:

  skywire cli skychat alias add claude2 02f9aa588dffa2...
  skywire cli skychat send -t claude2 -m "hi"
  skywire cli skychat history --peer claude2
  skywire cli skychat listen --from claude2

Full hex PKs always resolve directly, regardless of any same-named
alias (so a typo on a real PK never gets silently rerouted).`,
}

var aliasAddCmd = &cobra.Command{
	Use:   "add NAME PK",
	Short: "Add or replace an alias",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		name := strings.TrimSpace(args[0])
		pkStr := strings.TrimSpace(args[1])
		if name == "" {
			internal.PrintFatalError(cmd.Flags(), errors.New("alias name is empty"))
		}
		// Reserved prefix: a name starting with a hex digit could be
		// mistaken for a partial PK. Rejecting outright avoids the
		// "did you mean to type the full PK?" failure mode.
		if name[0] >= '0' && name[0] <= '9' {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("alias name %q must not start with a digit (looks like a PK fragment)", name))
		}
		var pk cipher.PubKey
		if err := pk.Set(pkStr); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid PK %q: %w", pkStr, err))
		}
		s, err := loadAliases()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		prev, existed := s.entries[name]
		s.entries[name] = pk.Hex()
		if err := s.save(); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		out := cmd.OutOrStdout()
		if existed {
			fmt.Fprintf(out, "alias %s replaced (was %s, now %s)\n", name, prev, pk.Hex()) //nolint:errcheck
		} else {
			fmt.Fprintf(out, "alias %s → %s\n", name, pk.Hex()) //nolint:errcheck
		}
	},
}

var aliasLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List all aliases",
	Run: func(cmd *cobra.Command, _ []string) {
		s, err := loadAliases()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		jsonMode, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck
		out := cmd.OutOrStdout()
		if jsonMode {
			b, _ := json.MarshalIndent(s.entries, "", "  ") //nolint:errcheck
			_, _ = out.Write(append(b, '\n'))               //nolint:errcheck
			return
		}
		if len(s.entries) == 0 {
			fmt.Fprintln(out, "(no aliases — use `skywire cli skychat alias add NAME PK` to create one)") //nolint:errcheck
			return
		}
		names := make([]string, 0, len(s.entries))
		for n := range s.entries {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(out, "%-20s %s\n", n, s.entries[n]) //nolint:errcheck
		}
	},
}

var aliasRmCmd = &cobra.Command{
	Use:   "rm NAME",
	Short: "Remove an alias",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := strings.TrimSpace(args[0])
		s, err := loadAliases()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if _, ok := s.entries[name]; !ok {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("no such alias %q", name))
		}
		delete(s.entries, name)
		if err := s.save(); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "alias %s removed\n", name) //nolint:errcheck
	},
}

func init() {
	aliasCmd.AddCommand(aliasAddCmd, aliasLsCmd, aliasRmCmd)
}
