# Login Chain — Blockchain-Based Wallet Ownership Verification

The login chain is an ephemeral [fibercoin](https://github.com/skycoin/skycoin/blob/develop/cmd/newcoin/README.md) used to verify that a user controls the private key for a given Skycoin reward address. It runs as a separate process alongside the reward system UI server.

## Architecture

Two skycoin nodes run locally:

| Component | P2P Port | API Port | Role |
|-----------|----------|----------|------|
| Block publisher | 6001 | 6421 | Signs genesis block, publishes new blocks, creates transactions |
| Peer node | 6002 | 6422 | Receives blocks, serves wallet API (CSRF disabled) for browser access |

The block publisher needs at least one peer to publish blocks to. The peer node provides that target and also serves the web wallet API with CSRF disabled for browser-based wallet interactions.

The reward system UI server connects to the publisher node (`--login-node http://127.0.0.1:6421`) for creating funding transactions, and proxies the peer node's API for the skycoin web wallet.

## Usage

Both commands must run from the same working directory (where `log_backups/` and `log_collection/` live).

```bash
# Terminal 1: start the login chain
skywire cli rewards loginchain

# Terminal 2: start the reward system UI
skywire cli rewards ui --login-node http://127.0.0.1:6421
```

## Startup Sequence

1. **Genesis wallet** (`login_genesis.json`) — generated on first run via `skycoin cli addressGen`. Persists across restarts so the genesis address stays the same.

2. **Blockchain data wiped** — `login_data/` and `login_peer_data/` are deleted on every startup. The chain is ephemeral; coins have no real value.

3. **Publisher starts** with `GENESIS=login_genesis.json` and `FIBER_TOML=login_fiber.toml`. It creates the genesis block, signs it, and writes the genesis signature back to `login_fiber.toml`.

4. **Peer config derived** — after the publisher is healthy, `login_peer.toml` is created from the updated `login_fiber.toml` (which now contains the signed genesis credentials) with ports swapped. The peer does NOT receive the `GENESIS` env var — that would clear the genesis signature.

5. **Peer starts** and syncs the genesis block from the publisher.

6. **Bootstrap (block #1)** — `createRawTransaction` sends all genesis coins back to the genesis address, creating a proper UxOut with a valid `SrcTransaction`. The genesis UxOut has a null `SrcTransaction` which the skycoin transaction API cannot handle. The raw transaction is injected via `/api/v1/injectTransaction` with `no_broadcast: true` (P2P between localhost nodes is unstable due to mirror detection). The block publisher includes the transaction in the next block.

## Authentication Flow

### 1. Address Lookup

User submits their reward address (Skycoin address or BIP44 xpub key) on `/login`. The server searches `log_backups/` for visor surveys with a matching `skycoin_address` field.

### 2. Login Address Derivation

- **Skycoin address**: used directly as the login address.
- **Xpub key**: the server derives a change chain address (`m/account'/1/0`) using `DeriveLoginAddressFromXpub`. This ensures login addresses never collide with reward addresses (which use the external chain `m/account'/0/i`).

### 3. Funding

The server sends 1 coin from the genesis wallet to the user's login address on the login chain using `createRawTransaction` (which loads `login_genesis.json` as a wallet file for signing).

### 4. Verification

The user opens the skycoin web wallet connected to the login chain peer node and sends the coin back to the genesis address. This proves they control the private key for the login address (and therefore the reward wallet).

The `/login/verify` page polls `/login/check` every 3 seconds, looking for a confirmed transaction from the user's login address to the genesis address.

### 5. Session

On confirmation, a session cookie is set (24-hour expiry). The `/account` page shows all visors associated with the user's reward address.

## Files

| File | Persists across restarts | Description |
|------|--------------------------|-------------|
| `login_genesis.json` | Yes | Genesis wallet in `addressGen` format (`{entries: [{address, public_key, secret_key}]}`) |
| `login_fiber.toml` | Regenerated | Publisher node fiber config — updated with genesis signature on startup |
| `login_peer.toml` | Regenerated | Peer node fiber config — derived from signed publisher config |
| `login_data/` | Wiped | Publisher blockchain data directory |
| `login_peer_data/` | Wiped | Peer blockchain data directory |

## Configuration

The `loginchain` subcommand uses the `--wd` flag (or current directory) for all file paths. The publisher flags are hardcoded; the peer node flags can be overridden via `--login-chain-flags` or the `LOGINCHAIN_FLAGS` environment variable in the SKYENV conf file.

Default peer flags:
```
--localhost-only --download-peerlist=false --disable-csrf
--host-whitelist=fiber.skywire.dev --enable-all-api-sets=true --log-level=warn
```

## Known Limitations

- **Localhost P2P instability**: two nodes on 127.0.0.1 trigger skycoin's mirror detection (`"Connection exists with this base IP and mirror"`), causing repeated connect/disconnect cycles. Blocks still propagate despite the noise. Using different LAN IPs would resolve this.

- **Genesis UxOut**: the skycoin `/api/v2/transaction` endpoint rejects genesis UxOuts (`SrcTransaction is not initialized`). The bootstrap step works around this by creating block #1 before any API transactions.

- **`distributeGenesis`**: the standard fibercoin distribution command has issues with coin disappearance. `createRawTransaction` is used instead for the bootstrap.
