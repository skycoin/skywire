// Package db internal/db/users.go
package db

import (
	"database/sql"
	"fmt"
	"time"
)

// CreateUser creates or updates a user's wallet addresses.
func (d *Database) CreateUser(user *User) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	user.UpdatedAt = time.Now().UTC()

	_, err := d.db.Exec(`
		INSERT INTO users (pubkey, wallet_sky, wallet_btc, wallet_bch, wallet_ltc, wallet_usdt_erc20, wallet_usdt_trc20, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(pubkey) DO UPDATE SET
			wallet_sky = excluded.wallet_sky,
			wallet_btc = excluded.wallet_btc,
			wallet_bch = excluded.wallet_bch,
			wallet_ltc = excluded.wallet_ltc,
			wallet_usdt_erc20 = excluded.wallet_usdt_erc20,
			wallet_usdt_trc20 = excluded.wallet_usdt_trc20,
			updated_at = excluded.updated_at
	`, user.PubKey, user.WalletSKY, user.WalletBTC, user.WalletBCH, user.WalletLTC,
		user.WalletUSDT_ERC20, user.WalletUSDT_TRC20, user.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create/update user: %w", err)
	}

	return nil
}

// GetUser retrieves a user by their public key.
func (d *Database) GetUser(pubkey string) (*User, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	user := &User{}
	err := d.db.QueryRow(`
		SELECT pubkey, wallet_sky, wallet_btc, wallet_bch, wallet_ltc, wallet_usdt_erc20, wallet_usdt_trc20, updated_at
		FROM users
		WHERE pubkey = ?
	`, pubkey).Scan(&user.PubKey, &user.WalletSKY, &user.WalletBTC, &user.WalletBCH,
		&user.WalletLTC, &user.WalletUSDT_ERC20, &user.WalletUSDT_TRC20, &user.UpdatedAt)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // User not found
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	return user, nil
}

// GetUserWallet returns the wallet address for a specific currency for a user.
func (d *Database) GetUserWallet(pubkey, currency string) (string, error) {
	user, err := d.GetUser(pubkey)
	if err != nil {
		return "", err
	}
	if user == nil {
		return "", fmt.Errorf("user not found: %s", pubkey)
	}

	switch currency {
	case "BTC":
		return user.WalletBTC, nil
	case "BCH":
		return user.WalletBCH, nil
	case "LTC":
		return user.WalletLTC, nil
	case "USDT_ERC20":
		return user.WalletUSDT_ERC20, nil
	case "USDT_TRC20":
		return user.WalletUSDT_TRC20, nil
	default:
		// SKY and every Skycoin fibercoin share the one registered Skycoin-family
		// address, so any sell-coin symbol resolves to it. This is where a seller
		// receives refunds and a buyer receives the purchased coin.
		return user.WalletSKY, nil
	}
}
