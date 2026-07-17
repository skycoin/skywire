# skycoin-web multicoin over the visor — the `/coin/<index>` server-proxy model

## The correct model (as skycoin-web already implements it)

skycoin-web is **already multicoin**. It does NOT need per-coin URLs baked into the
wallet. The mechanism (in the vendored source):

- `coin.service.ts` fetches the coin list from **`/api/v1/coins`** and builds
  `BaseCoin[]` via `BaseCoin.fromServerData` — each coin has an `id`, a
  `nodeUrl`, a `coinType` (`skycoin` | `bitcoin` | `bitcoin-segwit`), symbol,
  hours name, price-ticker id, explorer, etc.
- `api.service.ts` sends every request for the current coin to
  `customNodeUrls[coin.id] || coin.nodeUrl` — i.e. the coin's **own `nodeUrl`
  prefix**.
- The wallet already understands bitcoin coins (`isBitcoin()` → 8 decimals, no
  Coin Hours, skycoin-lite signing). BTC is just a coin with
  `coinType: 'bitcoin'`.

So the **server** (normally the skycoin daemon; here **the visor**) is the coin
router: it serves `/api/v1/coins` and proxies `/coin/<index>/…` to each coin's
backend. The wallet source needs **no change**.

### Why the earlier attempt was wrong (and is being reworked)

#2940 / #3493 put the backend into the coin's `nodeUrl` directly (e.g. an
`ssl://` electrum server in the URL) and bolted BTC on with a special
`/v1/btc/` fetch-intercept in the visor's `/wallet/` shim. That diverges from
skycoin-web's model, doesn't generalise past one extra coin, and left a
`TODO(config): multi-coin /coin/N` in `hypervisor_handlers_wallet.go`. This
rework replaces it with the native `/coin/<index>` model.

## Coin registry (visor-side)

A small ordered registry, each entry a `BaseCoin` record returned by
`/api/v1/coins` with `nodeUrl = "/coin/<index>"`:

| index | coinType | backend the visor proxies `/coin/<index>/…` to |
|---|---|---|
| 0 | `skycoin` | the skycoin node, over dmsg (the deployment `skycoin_node_dmsg`, or the operator's Settings→Nodes URL) |
| 1 | `bitcoin` | the in-visor **electrum gateway** (`pkg/btcgateway`), dialed ssl:// over the mesh via skysocks-lite |

Extensible: adding a coin = one registry entry + its backend route. `serverWallets:false` for all (the browser holds keys/does signing).

## Visor implementation

**Native visor** (`pkg/visor/hypervisor_handlers_wallet.go`):
- `GET /api/v1/coins` → serialize the registry (JSON array of `BaseCoin`).
- `/coin/<index>/*` → strip the prefix, route the remainder to the registry
  entry's backend:
  - index 0 → the existing dmsg node proxy (`walletNodeProxy`).
  - index 1 → the existing BTC electrum gateway (`nativeBtcGateway`).
- **Remove** the `/v1/btc/` special-case intercept (now just `/coin/1/api/v1/btc/…`).
- Keep the operator's Settings→Nodes override working: `customNodeUrls[0]` (a
  full dmsg/clearnet URL) overrides the default skycoin node for `/coin/0`.

**wasm visor** (`cmd/skywire-cli/commands/hv/serve.go` shim + the in-tab
`skywireVisor`):
- `/api/v1/coins` → served by the tab (same registry, static JSON).
- `/coin/<index>/…` → the shim routes to `skywireVisor.fetchDmsg` (index 0) or
  `skywireVisor.btcFetch` (index 1); drop the `/v1/btc/` substring special-case.

Both paths converge on the same registry definition (one Go source of truth,
mirrored to the tab as JSON) so native + wasm serve identical coins.

## What stays unchanged

- **The wallet source** — skycoin-web already does `/api/v1/coins` + per-coin
  `nodeUrl`. (The unrelated onboarding-disclaimer reword + the embedded
  wizard-skip are separate.)
- `pkg/btcgateway` (the electrum→HTTP gateway) — reused as the `/coin/1` backend.
- Key handling / signing — always browser-side (skycoin-lite WASM).

## Migration / compatibility

- Bare `/api/v1/*` (no `/coin/` prefix) → treat as `/coin/0` (default coin) so an
  un-migrated wallet build still reaches skycoin.
- The `/v1/btc/*` intercept is deleted once the registry lists BTC at `/coin/1`.

## Open decisions

1. **Registry location**: a new `pkg/skyenv` (or `pkg/wallet/coins`) Go slice, so
   native + wasm + config-gen share it.
2. Whether the **coin list is operator-configurable** (add coins via config) or
   fixed to {skycoin, bitcoin} for now — start fixed, make it config later.
3. Per-coin node override UX lives in skycoin-web's **Settings→Nodes**
   (`customNodeUrls`) — the outer visor wallet-config panel slims to transport-only.
