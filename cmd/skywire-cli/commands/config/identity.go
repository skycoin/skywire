// Package cliconfig cmd/skywire-cli/commands/config/identity.go c4-vis-cli
//
// `skywire cli config identity` — export the keypair a config file holds, or
// install a secret key into it. The desk's identity window drives this for a
// browser visor (#4484 stage 5): the tab's key lives in the visor's own config
// file in the exec worker's filesystem, which the running visor's RPC never
// reveals (GetRuntimeConfig redacts sk), so the file is read here.
package cliconfig

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var (
	identityPath string
	identitySK   string
)

func init() {
	identityCmd.PersistentFlags().StringVarP(&identityPath, "input", "i", "", "config file (default: this install's config path)")
	identityImportCmd.Flags().StringVar(&identitySK, "sk", "", "secret key to install (64 hex characters)")
	identityCmd.AddCommand(identityExportCmd)
	identityCmd.AddCommand(identityImportCmd)
	RootCmd.AddCommand(identityCmd)
}

var identityCmd = &cobra.Command{
	Use:   "identity",
	Short: "Export or import the keypair a config file holds",
	Long: `Read or replace the identity in a config file — the pk/sk pair that
makes a visor the same visor across restarts. 'export' prints them;
'import --sk' installs a secret key (and the public key derived from
it). A running visor picks the change up on its next start.`,
}

// Identity is the exported keypair.
type Identity struct {
	PK cipher.PubKey `json:"pk"`
	SK cipher.SecKey `json:"sk"`
}

func identityFile() string {
	if identityPath != "" {
		return identityPath
	}
	return visorconfig.SkywireConfig()
}

var identityExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Print the config file's public and secret key as JSON",
	Run: func(cmd *cobra.Command, _ []string) {
		raw, err := os.ReadFile(identityFile()) //nolint:gosec
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		var id Identity
		if err := json.Unmarshal(raw, &id); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("%s: %w", identityFile(), err))
		}
		if id.SK.Null() {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("%s holds no secret key", identityFile()))
		}
		out, _ := json.MarshalIndent(id, "", "  ") //nolint:errcheck
		internal.PrintOutput(cmd.Flags(), id, string(out)+"\n")
	},
}

var identityImportCmd = &cobra.Command{
	Use:   "import --sk <hex>",
	Short: "Install a secret key (and its public key) into the config file",
	Run: func(cmd *cobra.Command, _ []string) {
		var sk cipher.SecKey
		if err := sk.Set(identitySK); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--sk: %w", err))
		}
		pk, err := sk.PubKey()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--sk: %w", err))
		}
		path := identityFile()
		raw, err := os.ReadFile(path) //nolint:gosec
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		// Edit the two fields in place; every other field of the file is kept
		// byte-for-byte as JSON re-serializes it.
		var conf map[string]json.RawMessage
		if err := json.Unmarshal(raw, &conf); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("%s: %w", path, err))
		}
		pkJSON, _ := json.Marshal(pk) //nolint:errcheck
		skJSON, _ := json.Marshal(sk) //nolint:errcheck
		conf["pk"] = pkJSON
		conf["sk"] = skJSON
		out, err := json.MarshalIndent(conf, "", "  ")
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), Identity{PK: pk, SK: sk}, fmt.Sprintf("Installed identity %s into %s\n", pk.Hex(), path))
	},
}
