# skycoin-web wallet architecture — serving, custody, multicoin

Extends `gui-app-serving-modes.md` (the wallet-is-a-feature vs backend-is-an-app
distinction) with the **custody model** and the **seed-centric multicoin** model.
One mental model that reads identically on a native HV, a served standalone PWA
(`hv serve`), and a browser-tab wasm visor.

## 1. Layers — what varies vs what the operator chooses

The wallet **frontend is always a browser SPA** (Angular + `skycoin-lite.wasm`).
Two things vary by platform and the operator should never think about them:

- **How the frontend is served** — native Go handler (`walletHandler`) / static
  embedded assets (`hv serve`, wasm) / inlined single-file (`hv gen`).
- **How the node API is reached** — server-side dmsg proxy (native) / in-tab
  fetch-over-dmsg (wasm/standalone). This is the `/coin/<index>` seam.

The **one axis the operator actually chooses is custody** (where keys + wallet
files live).

## 2. Custody — `browser | disk | remote`, capability-gated

| Custody | Storage | Available where | Trust |
|---|---|---|---|
| **browser** (default) | localStorage (native/served) / OPFS virtual-dir (wasm) | **everywhere** — the SPA is always a browser | host never sees keys (zero-trust) |
| **disk** | real filesystem `wallet.dir` via the `skycoin-web` **server** (`RunSkycoinWeb`) | only where a backend Go process + FS exists: native visor, `hv serve` | host holds keys |
| **remote** | on another visor's `skycoin-web` server, over dmsg | **everywhere** (a dmsg target) | trust the remote |

**Capability gating is the crux of making sense across contexts:** a single-file
HV or a statically-hosted wasm PWA has no backend filesystem → offers only
`browser` + `remote`. A native visor or `hv serve` additionally offers `disk`.
The config is uniform; the *offered* options degrade to what the context realizes.

## 3. One config block, three realizations

```jsonc
wallet: {
  serve:     true,                 // serve the /wallet/ frontend at all (feature on/off)
  custody:   "browser",            // browser | disk | remote
  dir:       "~/.skycoin/wallets", // disk only: real path (seed-wallet STORE, not per-coin)
  user:      "operator",           // disk only: UID drop (SKYCOINWEBUSER) when visor runs as _skywire
  remote_pk: "",                   // remote only
}
```

- **Native visor / `hv serve`:** all three custody modes; `disk` runs
  `RunSkycoinWeb` with `wallet.dir`.
- **wasm / single-file:** `custody` clamped to `browser|remote`; browser = OPFS.
- **`RunSkycoinWeb` app:** it *is* the `disk`-custody implementation. It gets no
  *separate* enable — choosing `disk` (with a dir) is what turns it on. The Apps
  tab shows it running/stoppable; `wallet.custody` is the source of truth.

The existing `skycoinweb*` config-gen flags (`--skycoinweb`, `--skycoinwebwallet`,
`--skycoinwebaddr`, `--skycoinwebnodes`, `--skycoinwebuser`) become aliases into
this block.

## 4. Surfaces are views of the one block
- **GUI** (`/wallet/config` panel, any context): Storage selector Browser / This
  host (disk) / Remote; `dir` field revealed only for disk-and-capable; a security
  note that disk flips the trust model to host-holds-keys. Writes back via
  config-update. Subsumes today's browser-vs-remote toggle.
- **CLI** (`config gen` + `hv serve`): `--wallet[=false]` (serve), `--wallet-custody`,
  `--wallet-dir`, `--wallet-remote`.
- **Apps tab / internal-app:** the `skycoin-web` server = the disk-custody backend;
  enable == `custody==disk`; its settings show the same `dir`. No parallel knob to drift.

## 5. Multicoin — seed-centric, NO wallet file-format change

### The crypto (verified in vendored `cipher`)
- Skycoin address = `RIPEMD160(SHA256(SHA256(pubkey)))`; Bitcoin P2PKH =
  `RIPEMD160(SHA256(pubkey))`. **Addresses are NOT convertible between chains;
  the only shared thing is the KEY.**
- **Fibercoins (skycoin + every fork) share the address format AND keys** (same
  `cipher`, `coinType:"skycoin"`) — no fork changes the address version byte
  (it's a compile-time `cipher` constant, not a fiber-config param; verified
  against the reward **login chain**, which only sets `bip44_coin = 8001` +
  genesis/ticker, never the version byte).

### The format already supports multi-chain in one file
The bip44 wallet serializes `"accounts": [ … ]` where **each account carries its
own `"coin_type"`** ("determines the way to generate addresses"). Only `Meta.coin`
/ `Meta.bip44Coin` are single — a default, not a constraint. So one seed → one file
→ `accounts:[{coin_type:8000},{coin_type:0/BTC},{coin_type:8001/login}]` natively.

**Therefore: no new format, and NOT one-file-per-chain.** A wallet is a **seed**;
"a coin" is an **account (coin_type) inside it**.

- **Deterministic wallets** (coin-agnostic): one address set, **shared across ALL
  fibercoins** — query different nodes for per-coin balances. Cannot do BTC/foreign
  (no coin_type level).
- **BIP44 wallets:** each `coin_type` is its own branch off the one seed
  (skycoin 8000, login 8001, BTC 0…) — SLIP-44-correct, no cross-coin address
  linkage. The reward login flow already demonstrates both (deterministic address
  funded directly; xpub derived at `m/44'/coin'/0'/1/0`).

### What changes (logic/UI, shared by browser AND disk custody)
1. Lift the single-coin assumption in wallet-create/API + `MetaCoin` gating so a
   wallet holds accounts of different `coin_type`s (`newBip44Account(coinType)`
   already supports it).
2. Reshape skycoin-web's coin selector: "which coin" = which coin_type account in
   the *current* wallet, not a separate wallet/dir. "Activate a chain" = add its
   coin_type account.
3. Deterministic stays fibercoin-shared; bip44 is the foreign-chain vehicle.
4. Per-coin-dir demoted to a legacy import path.

Both custody paths (client-side skycoin-lite WASM, server-side `RunSkycoinWeb`)
use the same wallet lib, so this one change fixes both.

## 6. Settled decisions
- Custody axis browser|disk|remote, capability-gated; **default browser**.
- Wallet-dir lives in the GUI Storage section, revealed only in disk mode; it is a
  **seed-wallet store** (one dir, N seed files), never per-coin.
- Frontend serve is a CLI/config knob (`--wallet`), not a GUI self-toggle.
- `disk` selection requires an explicit "start the wallet server" confirmation
  (it flips the trust model).
- `hv serve` MAY offer disk (it has a FS) but defaults to browser to keep the
  standalone/serverless story clean.

## 7. Implementation staging
1. **skywire, self-contained:** `wallet.*` config block (+ `skycoinweb*` aliases),
   `hv serve --wallet`, GUI Storage section (browser/disk/remote + gated dir),
   config-update writeback. No wallet-model change yet — disk still single-coin.
2. **skycoin-web (skycoin repo):** lift the single-coin assumption; coin selector =
   coin_type-account-in-wallet; seed-centric create. Re-vendor + re-embed.
3. **skywire backend align:** `RunSkycoinWeb` seed-wallet dir (drop per-coin-dir);
   `/coin/<index>` routing keyed by coin_type. Legacy per-coin-dir import.

## 8. Future — adopt the Android wallet UX
The operator prefers the **skycoin-android** wallet GUI to skycoin-web's. It's
native Java over the Go lib (not portable code), so "adopt" = re-implement its
UX/layout in the Angular skycoin-web app. Keep the seed-centric model above
UI-agnostic so a reskin is a view change, not a model change. Separate effort;
does not block the model work.
