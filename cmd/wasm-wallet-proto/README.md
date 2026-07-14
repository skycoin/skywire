# wasm-wallet-proto — Gap A prototype (skycoin-web multicoin RFC)

Throwaway prototype validating **Gap A** of
`docs/design/skycoin-web-multicoin-wallets.md`: bridge "there are no dirs in the
browser" so skycoin's own wallet code runs, unchanged, in the wasm visor.

## Result (validated)

- `github.com/skycoin/skycoin/src/wallet` (+ `bip44wallet`) **compiles and runs
  under `js/wasm`** as-is. The `readable → visor → bbolt` chain that blocks
  `cmd/skycoin-web` is only the *coin-discovery* path, not wallet storage.
- **One seed → many chains** (seed-centric multicoin): the same BIP39 mnemonic
  yields a Skycoin address AND a Bitcoin address (distinct, correct formats).
- `.wlt` files are created, listed (`os.ReadDir`), saved (`wallet.Save`), and
  reloaded (`wallet.Load`) through a **virtual filesystem** — no real fs.
- **Persistence across sessions**: the virtual fs backs onto a KV store; in the
  browser that's `localStorage`/OPFS. A fresh session loads wallets a previous
  one saved.
- **Zero Go changes to skycoin.** The only new code is a ~160-line JS
  `globalThis.fs` shim at the syscall boundary (`wallet-fs-shim.js`).

## Validated two ways

- **node** (fast iteration): in-memory virtual fs + localStorage-style
  persistence across two processes.
- **real browser (Brave) with IndexedDB** (`index.html`): created a Skycoin +
  Bitcoin wallet from one seed, **reloaded the page**, and loaded both back from
  IndexedDB — persistence across real browser sessions. The actual target env.

## Run in a browser

    GOOS=js GOARCH=wasm go build -o d/wallet-proto.wasm ./cmd/wasm-wallet-proto/
    cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" d/
    cp cmd/wasm-wallet-proto/{wallet-fs-shim.js,index.html} d/
    (cd d && python3 -m http.server 8765)   # open http://localhost:8765/
    # console: await protoCreate(); location.reload(); await protoList()

`index.html` wires the fs shim's KV store to IndexedDB (load-before-boot,
save-on-fsync). In production this backing becomes OPFS/IndexedDB, encrypted at
rest.

## What this proves for the RFC

Gap A does **not** require reimplementing wallets in JS, nor decoupling the node
DB. `wallet.Service` is portable; the browser just needs a storage-backed
`globalThis.fs`. The remaining wiring is: mount this under the wasm visor's
skycoin-web app, back the shim onto OPFS/IndexedDB (encrypted at rest), and serve
the `/api/v1/coins` + `/coin/<index>` surface (the visor-as-server role).
