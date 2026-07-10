# Exchange Market

Skywire Exchange Market application - Order processing and blockchain integration

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

This application runs as a Skywire app integrated with the visor. It communicates with exchange-client applications via dmsg and manages the entire exchange process.

```bash
go build -o exchange-market
./exchange-market
```

## Security

- All data automatically deleted after 3 days for privacy
- Single session per public key
- Escrow protects both buyers and sellers
- Automatic token return after 1 hour on cancellation/failure
