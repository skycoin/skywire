// Package clihv cmd/skywire-cli/commands/hv/gen.go
package clihv

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
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
	genCmd.Flags().Uint32Var(&genIndex, "index", 0, "with -c: re-derive keyring entry at this index (read-only); omit to mint+record the next one")
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
// minted from the visor config's KeyRing (-c), or "" (ephemeral).
//
// With -c and no explicit --index, it MINTS the next keyring key (deterministic,
// derived one-way from the visor key), records it in the config's KeyRing, and
// flushes — the wallet "derive a new address" behavior. With --index N it
// re-derives entry N read-only (idempotent regeneration; no config write).
// Either way it prints the derived address + PK (the PK to set as the visors'
// remote hypervisor in standalone mode).
func resolveKey(cmd *cobra.Command) (string, error) {
	switch {
	case genSK != "":
		var sk cipher.SecKey
		if err := sk.Set(genSK); err != nil {
			return "", err
		}
		return genSK, nil
	case genConf != "":
		conf, err := visorconfig.ReadFile(genConf)
		if err != nil {
			return "", err
		}
		var entry visorconfig.KeyEntry
		if cmd.Flags().Changed("index") {
			if entry, err = conf.DeriveKeyEntry(wasmhv.StandaloneKeyLabel, genIndex); err != nil {
				return "", err
			}
			cmd.Printf("re-derived keyring entry %d — address %s, PK %s\n", entry.Index, entry.Address, entry.PublicKey)
		} else {
			if entry, err = conf.MintKey(wasmhv.StandaloneKeyLabel); err != nil {
				return "", err
			}
			if err = conf.Flush(); err != nil {
				return "", err
			}
			cmd.Printf("minted keyring entry %d (recorded in %s) — address %s, PK %s\n", entry.Index, genConf, entry.Address, entry.PublicKey)
		}
		return entry.SecretKey, nil
	default:
		cmd.PrintErrln("note: no --sk or -c given; the file uses an ephemeral identity (a fresh key each load)")
		return "", nil
	}
}
