# Multicoin wallets for skycoin-web on the visor (RFC)

Status: draft / for discussion
Scope: skycoin-web (skycoin fork) wallet model + how it runs as a visor app on
the host-native visor and, by default, in the wasm (browser) visor.

## Problem

skycoin-web supports multiple coins, but the model was "implemented but not
thought out in advance": a wallet is `{seed, coinId}`, wallets are stored one
**dir per chain**, and the UI filters by `wallet.coinId === currentCoin.id`.
That shape exists for one reason — reverse-compatibility with running multiple
fibercoin full nodes and their existing on-disk wallet dirs. It was not designed
around the thing that actually makes multicoin powerful: **one seed is a wallet
on many chains**.

Two things are entangled and worth separating:

1. **Storage** — how/where wallets are persisted (dirs vs browser storage).
2. **Wallet model** — coin-centric (`{seed, coin}` per file) vs seed-centric
   (a seed, viewable on N chains).

## What the rest of the world does (research)

Modern HD wallets are seed-centric. One BIP39 mnemonic derives *every* chain via
the BIP44 path `m / purpose' / coin_type' / account' / change / address_index`;
the `coin_type'` level (SLIP-44 registry — BTC `0'`, ETH `60'`, Skycoin `8000'`)
is exactly what lets one seed serve many chains with no key collision. The seed
is **chain-agnostic**; the wallet "file" is *metadata* — which chains/accounts
are enabled, labels, address book — recoverable/rebuildable from the mnemonic.
(See sources at the end.)

skycoin's on-disk format is the outlier: one `MetaSeed` **and** one
`MetaCoin`/`MetaBip44Coin` per file → one coin per wallet. But its
`bip44Account` struct already carries `CoinType` **per account**, so the data
model can represent several chains in one wallet — only the wallet `Meta` and the
`wallet/create` API assume a single coin. The structure is ~80% of the way to
seed-centric.

## The two gaps

### Gap A — storage ("there are no dirs in the browser")

Standalone skycoin-web (server-wallet mode) reads/writes `.wlt` files in
`--wallet-dir`. The browser has no filesystem, so today the wasm wallet is a
*separate* client-side path (simplified wallet object in localStorage, signing in
skycoin-lite WASM) rather than the same code the standalone runs.

If browser storage presented as a **virtual wallet dir** (OPFS / IndexedDB
backing the same `.wlt` layout), the standalone wallet-management path would
translate ~1:1: the visor plays the server, wallets live in the browser's
virtual dir, and multicoin works exactly as it does standalone. No wallet-format
change required — this is the shortest path, and the remaining work is GUI
configuration surface that mirrors the CLI flags (`--node-url`,
`--btc-electrum-url`, `--wallet-dir`/backend, `--socks5-proxy`).

This bridges the gap the operator identified: "if that small gap were bridged the
translation would be sensibly 1:1."

**VALIDATED by prototype (`cmd/wasm-wallet-proto/`).** `skycoin/src/wallet` (+
`bip44wallet`) compiles and runs under `js/wasm` unchanged — the
`readable → visor → bbolt` coupling that blocks `cmd/skycoin-web` is only the
coin-*discovery* path, not wallet storage. A ~160-line JS `globalThis.fs` shim
(`wallet-fs-shim.js`) backed by a KV store lets `wallet.Save`/`Load`/`os.ReadDir`
persist `.wlt` files; **one seed produced both a Skycoin and a Bitcoin address**.
Validated in **node** AND in a **real browser (Brave) backed by IndexedDB**:
created two wallets, reloaded the page, and loaded both back from IndexedDB —
persistence across real browser sessions, **zero Go changes to skycoin.** So Gap
A needs neither a JS wallet reimpl nor a node-DB decoupling; just a storage-backed
`fs` shim (browser: OPFS/IndexedDB, encrypted at rest).

### Gap B — model (one seed, many chains)

The seed-centric improvement. A wallet becomes a **seed** with a set of
**activated chains**; each activated chain is a BIP44 account with that chain's
`coin_type`. The single-file representation stores `{seed, [activated coin_types],
per-chain accounts/scanned ranges, labels]}`, leveraging the per-account
`CoinType` skycoin already has.

Two ways to get there:

- **UI grouping first (no format change):** keep per-chain `.wlt` files, but
  group them in the UI by a **seed fingerprint** so the same seed across chains
  presents as one multi-chain wallet; "activate chain X" materializes X's
  per-chain file. Reverse-compatible, zero migration, ships on Gap A.
- **Single-file v2 (format change):** one wallet file, multiple `coin_type`
  accounts. Cleaner, but a migration and a real change to `Meta` + the
  create/list API. Do this once the UX is proven.

Recommendation: **UI-grouping first, single-file v2 later.**

### Privacy: chain activation is explicit

A wallet must not auto-query every chain. Querying an address on a chain leaks
that you expect value there, and it clutters the UI with empty chains. So a
wallet has a **home chain** and you **opt in** to others; the visor only queries
a chain's node when that chain is activated for that wallet. This one mechanism
serves both the privacy lever and the "don't show a wallet you'd never expect a
balance for" lever.

## Backward-compat line

- **deterministic** (skycoin legacy) wallets have no `coin_type` → stay
  single-chain, imported as-is.
- **bip44** wallets are the vehicle for multi-chain (they have `coin_type`).
- Existing per-chain files import as single-chain wallets; grouping is additive.

## The visor's role (unchanged by all of the above)

The visor is pure transport — no external process, no extra ports, wasm on by
default, shared with native "in the ways that matter":

- serves skycoin-web as an internal app (`skywire app skycoin web`,
  `RunSkycoinWeb`), configured for the mesh;
- `/api/v1/coins` lists coins with `/coin/<index>` proxy prefixes;
- routes `/coin/<i>/api/v1/*` to coin *i*'s node over dmsg/skysocks, and
  `/coin/<btc>/v1/btc/*` to the BTC gateway (electrum from server config);
- a **remote wallet backend** is just another configurable URL (a `.dmsg`
  address the visor proxies) — client-side keys by default, or a remote
  server-wallet backend when wanted.

Note: the earlier `ssl://`-in-`nodeUrl` mechanism (PRs #2940/#3493) diverged from
this `/coin/<index>` model and should be reworked to it; the node-identity status
bar, ungated node settings, and the onboarding language fix from those PRs stand
on their own.

## Configuration surface (all in skycoin-web; visor only moves bytes)

| What | Scope | CLI-flag analogue |
|---|---|---|
| Coin node (chain data) | per coin | `--node-url` (as `.dmsg`) |
| BTC electrum | the Bitcoin coin | `--btc-electrum-url` |
| Wallet backend (client / remote) | per wallet | `--wallet-dir` / remote |
| Activated chains | per wallet | (new) |

## Open questions

1. Wallet-backend selection — **per coin** or **per wallet**? (Leaning per
   wallet: it's a property of the seed/keys, not the chain.)
2. Virtual-dir backing store — OPFS vs IndexedDB; encryption at rest.
3. ~~Does bridging Gap A mean compiling `wallet.Service` to wasm … or a JS
   reimpl?~~ **ANSWERED (prototype):** `wallet.Service` compiles under wasm
   as-is; bridge is a storage-backed `globalThis.fs` shim, no reimpl, no node-DB
   decoupling.
4. Seed-fingerprint definition for UI grouping (must not leak the seed).

## Sources

- [BIP-44 (bitcoin/bips)](https://github.com/bitcoin/bips/blob/master/bip-0044.mediawiki)
- [Derivation paths (learnmeabitcoin)](https://learnmeabitcoin.com/technical/keys/hd-wallets/derivation-paths/)
- [Ledger — addresses & derivation paths](https://www.ledger.com/blog/understanding-crypto-addresses-and-derivation-paths)
- [Trezor — what is BIP44](https://trezor.io/learn/a/what-is-bip44)
