//go:build js && wasm

// Command wasm-wallet-proto is a throwaway prototype for Gap A of the
// skycoin-web multicoin RFC (docs/design/skycoin-web-multicoin-wallets.md):
// prove that skycoin's own wallet package (.wlt read/write, bip44 derivation)
// runs UNDER wasm, and that ONE seed yields distinct wallets on multiple chains
// (Skycoin + Bitcoin) — the seed-centric model — persisted through a wallet
// "dir" that, in the browser, is browser storage (Gap A's virtual dir).
//
// Milestone 1a: run against a real fs dir (node) to prove the wallet logic +
// os-file path work under wasm. Milestone 1b swaps the dir for a
// browser-storage-backed globalThis.fs (fsshim.js) with no Go changes.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall/js"

	"github.com/skycoin/skycoin/src/cipher/bip39"
	"github.com/skycoin/skycoin/src/wallet"
	"github.com/skycoin/skycoin/src/wallet/bip44wallet" // also registers the bip44 loader (init)
)

// walletDir is the "dir" wallets are stored in. On node it's a real path; in the
// browser it's whatever the fs shim maps (browser storage). Overridable from JS.
var walletDir = "/wallets"

// mkWallet creates a bip44 wallet for `coin` from `seed`, generates one address,
// saves it to walletDir, and returns the first address.
func mkWallet(seed, label, filename string, coin wallet.CoinType) (string, error) {
	w, err := bip44wallet.NewWallet(filename, label, seed, "",
		wallet.OptionCoinType(coin),
		wallet.OptionGenerateN(1),
	)
	if err != nil {
		return "", fmt.Errorf("new: %w", err)
	}
	if err := wallet.Save(w, walletDir); err != nil {
		return "", fmt.Errorf("save: %w", err)
	}
	entries, err := w.GetEntries()
	if err != nil {
		return "", fmt.Errorf("entries: %w", err)
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("no addresses generated")
	}
	return entries[0].Address.String(), nil
}

// run returns a Promise. The actual work runs in a goroutine so its blocking
// (async-under-wasm) fs syscalls yield back to the JS event loop — calling it
// synchronously would deadlock (the loop can't service the fs callbacks).
func run(this js.Value, args []js.Value) any {
	dir := walletDir
	if len(args) > 0 && args[0].Type() == js.TypeString {
		dir = args[0].String()
	}
	return js.Global().Get("Promise").New(js.FuncOf(func(_ js.Value, p []js.Value) any {
		resolve := p[0]
		go func() { resolve.Invoke(js.ValueOf(doWork(dir))) }()
		return nil
	}))
}

func doWork(dir string) map[string]any {
	walletDir = dir
	out := map[string]any{"dir": walletDir}

	if err := os.MkdirAll(walletDir, 0700); err != nil {
		out["mkdirErr"] = err.Error()
	}

	// One seed → two chains (the seed-centric thesis).
	seed, err := bip39.NewDefaultMnemonic()
	if err != nil {
		out["seedErr"] = err.Error()
		return out
	}
	out["seed"] = seed

	if a, err := mkWallet(seed, "My Skycoin", "sky.wlt", wallet.CoinTypeSkycoin); err != nil {
		out["skyErr"] = err.Error()
	} else {
		out["skyAddr"] = a
	}
	if a, err := mkWallet(seed, "My Bitcoin", "btc.wlt", wallet.CoinTypeBitcoin); err != nil {
		out["btcErr"] = err.Error()
	} else {
		out["btcAddr"] = a
	}

	// List the dir (proves readdir over the fs works).
	var listed []any
	if des, err := os.ReadDir(walletDir); err != nil {
		out["listErr"] = err.Error()
	} else {
		for _, de := range des {
			listed = append(listed, de.Name())
		}
	}
	out["files"] = listed

	// Load one back (proves round-trip through the fs).
	if w, err := wallet.Load(filepath.Join(walletDir, "sky.wlt")); err != nil {
		out["reloadErr"] = err.Error()
	} else {
		out["reloadedLabel"] = w.Label()
		out["reloadedCoin"] = string(w.Coin())
	}

	return out
}

// listOnly loads + summarizes the wallets already in walletDir — no creation.
// Proves persistence: a fresh session sees wallets saved by a previous one.
func listOnly(dir string) map[string]any {
	walletDir = dir
	out := map[string]any{"dir": dir}
	var wallets []any
	des, err := os.ReadDir(dir)
	if err != nil {
		out["listErr"] = err.Error()
		return out
	}
	for _, de := range des {
		w, err := wallet.Load(filepath.Join(dir, de.Name()))
		if err != nil {
			wallets = append(wallets, map[string]any{"file": de.Name(), "err": err.Error()})
			continue
		}
		entry := map[string]any{"file": de.Name(), "label": w.Label(), "coin": string(w.Coin())}
		if es, err := w.GetEntries(); err == nil && len(es) > 0 {
			entry["addr"] = es[0].Address.String()
		}
		wallets = append(wallets, entry)
	}
	out["wallets"] = wallets
	return out
}

func main() {
	js.Global().Set("walletProtoRun", js.FuncOf(run))
	js.Global().Set("walletProtoList", js.FuncOf(func(_ js.Value, a []js.Value) any {
		dir := "/wallets"
		if len(a) > 0 && a[0].Type() == js.TypeString {
			dir = a[0].String()
		}
		return js.Global().Get("Promise").New(js.FuncOf(func(_ js.Value, p []js.Value) any {
			go func() { p[0].Invoke(js.ValueOf(listOnly(dir))) }()
			return nil
		}))
	}))
	fmt.Println("[wasm-wallet-proto] ready — call walletProtoRun('<dir>')")
	select {}
}
