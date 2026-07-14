// Package db internal/db/config.go
package db

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/skycoin/skywire/internal/exchange-market/protocol"
)

// ExplorerKeyPrefix returns the market_config key prefix for a currency's
// explorer settings, e.g. "explorer_btc". The full keys are <prefix>_provider,
// <prefix>_url and <prefix>_key.
func ExplorerKeyPrefix(currency string) string {
	return "explorer_" + strings.ToLower(currency)
}

// configOrEmpty reads a config value, returning "" (not an error) when the key
// has never been set.
func (d *Database) configOrEmpty(key string) (string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var v string
	err := d.db.QueryRow(`SELECT value FROM market_config WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to get config %s: %w", key, err)
	}
	return v, nil
}

// ExplorerConfig returns the operator's configured explorer provider, optional
// base-URL override and API key for a currency. An empty provider means the
// currency has no explorer configured (and is therefore unavailable). Satisfies
// chain.ExplorerConfigStore.
func (d *Database) ExplorerConfig(currency string) (provider, baseURL, apiKey string, err error) {
	p := ExplorerKeyPrefix(currency)
	if provider, err = d.configOrEmpty(p + "_provider"); err != nil {
		return "", "", "", err
	}
	if baseURL, err = d.configOrEmpty(p + "_url"); err != nil {
		return "", "", "", err
	}
	if apiKey, err = d.configOrEmpty(p + "_key"); err != nil {
		return "", "", "", err
	}
	return provider, baseURL, apiKey, nil
}

// IsCurrencyAvailable reports whether currency is supported and has an explorer
// provider configured.
func (d *Database) IsCurrencyAvailable(currency string) (bool, error) {
	if !protocol.IsSupportedCurrency(currency) {
		return false, nil
	}
	provider, _, _, err := d.ExplorerConfig(currency)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(provider) != "", nil
}

// AvailableCurrencies returns the supported currencies that have an explorer
// configured, in canonical display order.
func (d *Database) AvailableCurrencies() ([]string, error) {
	var out []string
	for _, c := range protocol.PaymentCurrencies {
		ok, err := d.IsCurrencyAvailable(c)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, c)
		}
	}
	return out, nil
}

// GetConfig retrieves a configuration value by key.
func (d *Database) GetConfig(key string) (string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var value string
	err := d.db.QueryRow(`
		SELECT value FROM market_config WHERE key = ?
	`, key).Scan(&value)

	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("config key not found: %s", key)
		}
		return "", fmt.Errorf("failed to get config: %w", err)
	}

	return value, nil
}

// GetConfigInt retrieves a configuration value as an integer.
func (d *Database) GetConfigInt(key string) (int, error) {
	value, err := d.GetConfig(key)
	if err != nil {
		return 0, err
	}

	result, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("config value is not an integer: %s = %s", key, value)
	}

	return result, nil
}

// GetConfigFloat retrieves a configuration value as a float.
func (d *Database) GetConfigFloat(key string) (float64, error) {
	value, err := d.GetConfig(key)
	if err != nil {
		return 0, err
	}

	result, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("config value is not a float: %s = %s", key, value)
	}

	return result, nil
}

// SetConfig sets a configuration value.
func (d *Database) SetConfig(key, value string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now().UTC()
	_, err := d.db.Exec(`
		INSERT INTO market_config (key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			updated_at = excluded.updated_at
	`, key, value, now)

	if err != nil {
		return fmt.Errorf("failed to set config: %w", err)
	}

	return nil
}

// GetAllConfig returns all configuration key-value pairs.
func (d *Database) GetAllConfig() (map[string]string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.Query(`
		SELECT key, value FROM market_config
		ORDER BY key
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to get all config: %w", err)
	}
	defer rows.Close() //nolint

	config := make(map[string]string)
	for rows.Next() {
		var key, value string
		err := rows.Scan(&key, &value)
		if err != nil {
			return nil, fmt.Errorf("failed to scan config: %w", err)
		}
		config[key] = value
	}

	return config, nil
}

// GetMarketWallet returns the market's SKY wallet address for escrow.
func (d *Database) GetMarketWallet() (string, error) {
	return d.GetConfig("wallet_sky")
}

// GetFreezeViolationsLimit returns the number of violations before a user is banned.
func (d *Database) GetFreezeViolationsLimit() (int, error) {
	return d.GetConfigInt("freeze_violations_limit")
}

// GetBanDurationDays returns the ban duration in days.
func (d *Database) GetBanDurationDays() (int, error) {
	return d.GetConfigInt("ban_duration_days")
}

// GetListingExpiryMinutes returns how many minutes a pending listing is valid for.
func (d *Database) GetListingExpiryMinutes() (int, error) {
	return d.GetConfigInt("listing_expiry_minutes")
}

// GetOrderExpiryMinutes returns how many minutes a pending order is valid for.
func (d *Database) GetOrderExpiryMinutes() (int, error) {
	return d.GetConfigInt("order_expiry_minutes")
}

// GetCleanupDays returns how many days after completion orders are deleted.
func (d *Database) GetCleanupDays() (int, error) {
	return d.GetConfigInt("cleanup_days")
}

// GetCommissionRatePercent returns the market commission rate as a percent of the
// SKY sold (default 0.5 = 0.5%).
func (d *Database) GetCommissionRatePercent() (float64, error) {
	return d.GetConfigFloat("commission_rate_percent")
}

// GetCommissionMinSKY returns the minimum SKY commission per sale (floor), so
// tiny trades still pay something (default 0.001).
func (d *Database) GetCommissionMinSKY() (float64, error) {
	return d.GetConfigFloat("commission_min_sky")
}

// GetCommissionMaxSKY returns the maximum SKY commission per sale (cap). Zero (or
// negative) means no cap.
func (d *Database) GetCommissionMaxSKY() (float64, error) {
	return d.GetConfigFloat("commission_max_sky")
}
