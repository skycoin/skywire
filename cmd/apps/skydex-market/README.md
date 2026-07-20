# SkyDEX - Market

SkyDEX - Market application - Order processing and blockchain integration

## Features

- 🔐 **Escrow Management** - Secure token custody for SKY
- 📊 **Order Processing** - Handle buy and sell orders
- 🔗 **Blockchain Integration** - Connect to SKY, BTC, BCH, LTC, USDT blockchains
- 🗄️ **SQLite Storage** - Lightweight database with automatic cleanup after 3 days
- 🚫 **Ban System** - Track and ban violating users
- 💰 **Commission Calculation** - 1 hour SCH fee calculation

## Architecture

### Background Jobs

1. **Escrow Checker** - Check payment confirmations (every 30 seconds)
2. **Listing Checker** - Verify seller token transfers (every 30 seconds)
3. **Expiry Handler** - Manage listing and order expirations (every 10 seconds)
4. **Return Scheduler** - Return tokens after 1 hour of cancellation/failure (every 1 minute)
5. **Cleanup Job** - Delete completed transaction data after 3 days (every 1 hour)
6. **Ban Manager** - Manage user bans and violations (every 1 minute)

### API

Communication with clients via dmsg using JSON-based protocol

### Database

SQLite database with 7 tables:
- `users` - User wallet addresses
- `pending_listings` - Pending seller token transfers
- `products` - Active sell orders
- `orders` - Active buy orders
- `freeze_violations` - Track freeze violations
- `bans` - Banned users
- `market_config` - Market configuration

## TODO

- [ ] Implement main.go with visor integration
- [ ] Connect to dmsg for client communication
- [ ] Implement SQLite database schema and migrations
- [ ] Build background jobs system
- [ ] Blockchain integration (SKY, BTC, BCH, LTC, USDT)
- [ ] Implement escrow management logic
- [ ] Calculate and collect commissions
- [ ] Session management with single-session enforcement
- [ ] Non-round number generation for payment validation

## Development

This application runs as a Skywire app integrated with the visor. It communicates with skydex-client applications via dmsg and manages the entire exchange process.

```bash
go build -o skydex-market
./skydex-market
```

## Operator UI authentication

The operator panel (`--addr`, default `:8050`) is gated by a **single-use one-time
code**. The market mints the code itself and publishes it to the visor, where it
appears on the `skydex-market` row of the hypervisor's app list.

To log in:

1. Open the hypervisor and find `skydex-market` in the app list.
2. Copy the 5-digit code from the **OTP** column.
3. Enter it on the market's login screen.

Each code works exactly once. A successful login mints and publishes a
replacement, so reloading the page requires a fresh code — nothing is kept in a
cookie, local storage, or session storage.

**Any wrong entry also burns the code.** A single failed attempt mints and
publishes a replacement, so after a typo you must return to the hypervisor app
list and read the new code. This is deliberate: the code is only five digits
(100,000 combinations), and rotating on every failure denies an attacker the
ability to work through that space in order. Login attempts are additionally
rate limited per source IP and globally.

> **Operator note.** Because a five-digit space is small, the global rate limiter
> is the control that actually bounds an attacker. It is set to one attempt per
> six minutes across all source addresses (240/day), so covering the whole
> 100,000-code space costs roughly 417 days of uninterrupted guessing. A single
> operator never notices: a login spends one token, and a burst of ten absorbs
> typos before the sustained rate applies. If this market holds substantially
> more value, raise `otpLen` — each extra digit multiplies the attacker's work
> by ten. A side effect of rotate-on-failure is that anyone able to
> reach the login endpoint can keep the code churning, which is a nuisance-level
> denial of service against operator login.

### Why a one-time code rather than a password

skychat and the hypervisor authenticate with a long-lived password stored as an
unstretched salted SHA-256 hash, with no rate limiting on login. That is a
reasonable trade for those surfaces; it is a poor one here, because this panel
can write the escrow hot-wallet seed. The one-time code avoids three properties
that made a password the wrong fit:

- **Nothing reusable crosses the wire.** A captured password grants permanent,
  silent access. A captured code is almost always already dead.
- **There is no credential at rest to crack.** No password hash exists to leak,
  so hash strength is moot. The code lives in memory and rotates on use and on
  repeated failure, so a brute-force search cannot converge.
- **No stored secret means no ambient credential.** The browser holds its session
  token in memory only, so a hostile page cannot make the browser replay it —
  CSRF is structurally impossible rather than something to defend against.

It also reuses a trust boundary that already exists. The hypervisor app list is
behind hypervisor auth, so the market gets an out-of-band channel for free
instead of introducing a second credential store to manage, rotate, and back up.

### Secrets are write-only

`sky_wallet_seed`, every `explorer_<coin>_key`, and each sell coin's
`wallet_seed` can be set through the operator API but are never returned by it.
`GET /api/config` reports only whether each is configured (`secrets_set`), and
submitting a blank value keeps the stored one rather than clearing it.

### Deployment caveats

- **The OTP inherits the hypervisor's authentication.** Anyone who can read the
  app list can log into the market. `enable_auth` is on by default; running the
  hypervisor without it makes this panel effectively public.
- **Plain HTTP is not sufficient on an untrusted network.** The session token is
  sent on every request, so anyone on the path can capture and replay it for the
  life of the session. Put a TLS-terminating proxy in front, or reach the panel
  over an SSH tunnel:

  ```bash
  ssh -L 8050:127.0.0.1:8050 user@server   # then browse http://localhost:8050
  ```

## Security

- All data automatically deleted after 3 days for privacy
- Single session per public key
- Escrow protects both buyers and sellers
- Automatic token return after 1 hour on cancellation/failure
- Operator UI gated by a single-use OTP (see above); escrow seeds are write-only
