// Package clihv cmd/skywire-cli/commands/hv/gen.go
package clihv

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/wasmhv"
)

var (
	genWasm     string
	genOut      string
	genConf     string
	genSK       string
	genIndex    uint32
	genPassword string
	genViewerPK string
	genSeedPK   string
	genSeedWS   string
	genDisc     string
)

func init() {
	genCmd.Flags().StringVar(&genWasm, "wasm", "", "path to the js/wasm dmsg-client binary (build: make dmsg-wasm) [required]")
	genCmd.Flags().StringVarP(&genOut, "out", "o", "hypervisor.html", "output file")
	genCmd.Flags().StringVarP(&genConf, "conf", "c", "", "visor config to derive the standalone key from (uses its sk)")
	genCmd.Flags().Uint32Var(&genIndex, "index", 0, "derivation index — distinct indices mint distinct standalone identities from one root")
	genCmd.Flags().StringVar(&genSK, "sk", "", "explicit standalone secret key (hex) — skips derivation")
	genCmd.Flags().StringVar(&genPassword, "password", "", "encrypt the baked-in key with this password (recommended)")
	genCmd.Flags().StringVar(&genViewerPK, "viewer-pk", "", "viewer mode: dial this remote hypervisor PK (default: standalone — visors dial in)")
	genCmd.Flags().StringVar(&genSeedPK, "seed-pk", "", "seed dmsg server PK the browser connects to first")
	genCmd.Flags().StringVar(&genSeedWS, "seed-ws", "", "seed dmsg server ws:// URL")
	genCmd.Flags().StringVar(&genDisc, "disc", "", "dmsg discovery (dmsg://<pk>:80)")
}

var genCmd = &cobra.Command{
	Use:   "gen",
	Short: "Generate a self-contained standalone hypervisor.html",
	Long: `Generate a self-contained, serverless hypervisor.html.

The output is a single file (Angular UI + WASM dmsg client + override.js + your
config, all inlined) you can open from file:// — no server. By default it is a
STANDALONE hypervisor (visors dial in); --viewer-pk makes it dial a remote
hypervisor instead.

Identity: --sk sets an explicit key; otherwise -c <config> derives a standalone
key from the visor's secret key (one-way + deterministic — regenerable, and a
compromised standalone key can't expose the parent). Use --password to encrypt
the baked-in key (the plaintext never touches the file).

SECURITY: never serve a generated (key-bearing) file from a domain — it would
train users to type secret keys into pages from external hosts.`,
	Run: func(cmd *cobra.Command, _ []string) {
		if genWasm == "" {
			cmd.PrintErrln("--wasm is required (build it with `make dmsg-wasm`)")
			os.Exit(1)
		}

		skHex, err := resolveKey(cmd)
		if err != nil {
			cmd.PrintErrln("identity:", err)
			os.Exit(1)
		}
		if skHex != "" && genPassword == "" {
			cmd.PrintErrln("warning: the secret key is baked in WITHOUT a password (plaintext on disk); pass --password to encrypt it")
		}

		wasm, err := os.ReadFile(genWasm) //nolint:gosec // operator-supplied path
		if err != nil {
			cmd.PrintErrln("read --wasm:", err)
			os.Exit(1)
		}
		uiFS, err := visor.HypervisorUIFS()
		if err != nil {
			cmd.PrintErrln("hypervisor UI assets:", err)
			os.Exit(1)
		}

		cfg := wasmhv.StandaloneConfig{
			Standalone:   genViewerPK == "",
			HypervisorPK: genViewerPK,
			SeedPK:       genSeedPK,
			SeedWS:       genSeedWS,
			Disc:         genDisc,
			SecretKey:    skHex,
			Password:     genPassword,
		}
		html, err := wasmhv.GenerateStandalone(uiFS, wasmhv.WasmExecJS, wasm, wasmhv.OverrideJS, cfg)
		if err != nil {
			cmd.PrintErrln("generate:", err)
			os.Exit(1)
		}
		if err := os.WriteFile(genOut, html, 0o600); err != nil {
			cmd.PrintErrln("write:", err)
			os.Exit(1)
		}
		cmd.Printf("wrote %s (%d bytes)\n", genOut, len(html))
	},
}

// resolveKey returns the standalone secret-key hex to bake in: explicit (--sk),
// derived from the config's visor key (-c [+--index]), or "" (ephemeral). When
// deriving, it prints the derived public key — the PK to set as the visors'
// remote hypervisor (standalone mode).
func resolveKey(cmd *cobra.Command) (string, error) {
	switch {
	case genSK != "":
		var sk cipher.SecKey
		if err := sk.Set(genSK); err != nil {
			return "", fmt.Errorf("bad --sk: %w", err)
		}
		return genSK, nil
	case genConf != "":
		parentSK, err := configSK(genConf)
		if err != nil {
			return "", err
		}
		pk, sk, err := wasmhv.DeriveStandaloneKey(parentSK, genIndex)
		if err != nil {
			return "", err
		}
		cmd.Printf("derived standalone hypervisor PK (index %d): %s\n", genIndex, pk.Hex())
		return sk.Hex(), nil
	default:
		cmd.PrintErrln("note: no --sk or -c given; the file uses an ephemeral identity (a fresh key each load)")
		return "", nil
	}
}

// configSK reads the visor secret key from a config file's top-level "sk".
func configSK(path string) (cipher.SecKey, error) {
	b, err := os.ReadFile(path) //nolint:gosec // operator-supplied path
	if err != nil {
		return cipher.SecKey{}, fmt.Errorf("read config %q: %w", path, err)
	}
	var c struct {
		SK string `json:"sk"`
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return cipher.SecKey{}, fmt.Errorf("parse config %q: %w", path, err)
	}
	if c.SK == "" {
		return cipher.SecKey{}, fmt.Errorf("config %q has no \"sk\"", path)
	}
	var sk cipher.SecKey
	if err := sk.Set(c.SK); err != nil {
		return cipher.SecKey{}, fmt.Errorf("config %q has invalid sk: %w", path, err)
	}
	return sk, nil
}
