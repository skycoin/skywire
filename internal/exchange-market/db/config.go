// Package db internal/db/config.go
package db

import (
	"database/sql"
	"fmt"
	"strconv"
	"time"
)

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

// GetReturnDelayHours returns how many hours to wait before returning SKY to seller.
func (d *Database) GetReturnDelayHours() (int, error) {
	return d.GetConfigInt("return_delay_hours")
}

// GetCleanupDays returns how many days after completion orders are deleted.
func (d *Database) GetCleanupDays() (int, error) {
	return d.GetConfigInt("cleanup_days")
}
