// Package db internal/db/database.go
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // SQLite driver (pure Go, no CGO)
)

// Database wraps the SQLite connection and provides methods for the exchange market.
type Database struct {
	db   *sql.DB
	path string
	mu   sync.RWMutex
}

// New creates a new database connection. If dbPath is empty, it defaults to
// <workDir>/exchange-market.db
func New(dbPath, workDir string) (*Database, error) {
	if dbPath == "" {
		if workDir == "" {
			workDir = "."
		}
		dbPath = filepath.Join(workDir, "exchange-market.db")
	}

	// Ensure the directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil { //nolint
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	// Open SQLite database with WAL mode for better concurrent performance
	dsn := fmt.Sprintf("file:%s?_journal=WAL&_timeout=5000&_fk=true", dbPath)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test the connection
	if err := conn.Ping(); err != nil {
		conn.Close() //nolint
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Set connection pool settings
	conn.SetMaxOpenConns(1) // SQLite works best with a single writer
	conn.SetMaxIdleConns(5)
	conn.SetConnMaxLifetime(time.Hour)

	return &Database{
		db:   conn,
		path: dbPath,
	}, nil
}

// Close closes the database connection.
func (d *Database) Close() error {
	if d.db != nil {
		return d.db.Close()
	}
	return nil
}

// Path returns the path to the database file.
func (d *Database) Path() string {
	return d.path
}

// Migrate runs all database migrations.
func (d *Database) Migrate() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	migrations := []string{
		createUsersTable,
		createPendingListingsTable,
		createProductsTable,
		createOrdersTable,
		createFreezeViolationsTable,
		createBansTable,
		createMarketConfigTable,
	}

	for _, migration := range migrations {
		if _, err := d.db.Exec(migration); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}

	// Evolve the schema for databases created before escrow-return tracking
	// existed. CREATE TABLE IF NOT EXISTS above is a no-op on such databases,
	// so the Return Scheduler's columns are added here.
	for _, c := range []struct{ col, ddl string }{
		{"closed_at", "DATETIME"},
		{"returned_at", "DATETIME"},
		{"return_tx_hash", "TEXT"},
	} {
		if err := d.ensureColumn("pending_listings", c.col, c.ddl); err != nil {
			return err
		}
	}
	// products.listing_id links a product back to the listing it was promoted
	// from, so a seller cancel can locate and refund the right escrow.
	if err := d.ensureColumn("products", "listing_id", "TEXT"); err != nil {
		return err
	}
	// orders.commission_sch records the Coin Hours commission booked on a
	// completed sale (floor(amount_sky * fee_rate)).
	if err := d.ensureColumn("orders", "commission_sch", "INTEGER DEFAULT 0"); err != nil {
		return err
	}

	return nil
}

// ensureColumn adds a column to a table if it is not already present. SQLite has
// no "ADD COLUMN IF NOT EXISTS", so a "duplicate column name" error (the column
// already exists) is treated as success, making this idempotent across runs.
func (d *Database) ensureColumn(table, column, ddl string) error {
	_, err := d.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, ddl))
	if err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("ensure column %s.%s: %w", table, column, err)
	}
	return nil
}

// InitDefaultConfig initializes default market configuration if not already present.
func (d *Database) InitDefaultConfig() error {
	defaults := map[string]string{
		"wallet_sky":              "",   // Market escrow address (must be the sky_wallet_seed's first address)
		"sky_fullnode_url":        "",   // SKY fullnode API base URL (native SKY verification + broadcast)
		"sky_wallet_seed":         "",   // Escrow hot wallet seed; spends (delivery/refund) are signed locally
		"fee_rate_sch_per_sky":    "1",  // Commission: Coin Hours (SCH) per SKY sold (1 = one hour's worth)
		"freeze_violations_limit": "3",  // Number of violations before ban
		"ban_duration_days":       "7",  // Ban duration in days
		"listing_expiry_minutes":  "15", // Minutes before pending listing expires
		"order_expiry_minutes":    "15", // Minutes before pending order expires
		"return_delay_hours":      "1",  // Hours before returning SKY to seller
		"cleanup_days":            "3",  // Days after which completed orders are deleted
	}
	// Per-coin explorer config keys are created on demand when the operator
	// configures a coin (see db.ExplorerConfig); they are not seeded here.

	d.mu.Lock()
	defer d.mu.Unlock()

	for key, value := range defaults {
		_, err := d.db.Exec(`
			INSERT OR IGNORE INTO market_config (key, value, updated_at) 
			VALUES (?, ?, ?)
		`, key, value, time.Now().UTC())
		if err != nil {
			return fmt.Errorf("failed to init config key %s: %w", key, err)
		}
	}

	return nil
}

// SQL migration statements

const createUsersTable = `
CREATE TABLE IF NOT EXISTS users (
    pubkey           TEXT PRIMARY KEY,
    wallet_sky       TEXT NOT NULL,
    wallet_btc       TEXT,
    wallet_bch       TEXT,
    wallet_ltc       TEXT,
    wallet_usdt_erc20 TEXT,
    wallet_usdt_trc20 TEXT,
    updated_at       DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_users_updated ON users(updated_at);
`

const createPendingListingsTable = `
CREATE TABLE IF NOT EXISTS pending_listings (
    id                  TEXT PRIMARY KEY,
    seller_pubkey       TEXT NOT NULL,
    amount_sky          REAL NOT NULL,
    expected_amount_sky REAL NOT NULL,
    price               REAL NOT NULL,
    payment_currency    TEXT NOT NULL,
    status              TEXT NOT NULL,
    expires_at          DATETIME NOT NULL,
    created_at          DATETIME DEFAULT CURRENT_TIMESTAMP,
    confirmed_at        DATETIME,
    tx_hash             TEXT,
    closed_at           DATETIME,
    returned_at         DATETIME,
    return_tx_hash      TEXT,
    FOREIGN KEY (seller_pubkey) REFERENCES users(pubkey)
);

CREATE INDEX IF NOT EXISTS idx_pending_listings_seller ON pending_listings(seller_pubkey);
CREATE INDEX IF NOT EXISTS idx_pending_listings_status ON pending_listings(status);
CREATE INDEX IF NOT EXISTS idx_pending_listings_expires ON pending_listings(expires_at);
`

const createProductsTable = `
CREATE TABLE IF NOT EXISTS products (
    id                  TEXT PRIMARY KEY,
    listing_id          TEXT,
    seller_pubkey       TEXT NOT NULL,
    amount_sky          REAL NOT NULL,
    price               REAL NOT NULL,
    payment_currency    TEXT NOT NULL,
    status              TEXT NOT NULL,
    created_at          DATETIME DEFAULT CURRENT_TIMESTAMP,
    frozen_at           DATETIME,
    frozen_by           TEXT,
    sold_at             DATETIME,
    FOREIGN KEY (seller_pubkey) REFERENCES users(pubkey)
);

CREATE INDEX IF NOT EXISTS idx_products_listing ON products(listing_id);

CREATE INDEX IF NOT EXISTS idx_products_status ON products(status);
CREATE INDEX IF NOT EXISTS idx_products_seller ON products(seller_pubkey);
`

const createOrdersTable = `
CREATE TABLE IF NOT EXISTS orders (
    id                      TEXT PRIMARY KEY,
    product_id              TEXT NOT NULL,
    buyer_pubkey            TEXT NOT NULL,
    amount_sky              REAL NOT NULL,
    price                   REAL NOT NULL,
    payment_currency        TEXT NOT NULL,
    expected_payment_amount REAL NOT NULL,
    seller_wallet           TEXT NOT NULL,
    status                  TEXT NOT NULL,
    expires_at              DATETIME NOT NULL,
    created_at              DATETIME DEFAULT CURRENT_TIMESTAMP,
    paid_at                 DATETIME,
    payment_tx_hash         TEXT,
    confirmations           INTEGER DEFAULT 0,
    completed_at            DATETIME,
    commission_sch          INTEGER DEFAULT 0,
    FOREIGN KEY (product_id) REFERENCES products(id),
    FOREIGN KEY (buyer_pubkey) REFERENCES users(pubkey)
);

CREATE INDEX IF NOT EXISTS idx_orders_buyer ON orders(buyer_pubkey);
CREATE INDEX IF NOT EXISTS idx_orders_product ON orders(product_id);
CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status);
CREATE INDEX IF NOT EXISTS idx_orders_expires ON orders(expires_at);
`

const createFreezeViolationsTable = `
CREATE TABLE IF NOT EXISTS freeze_violations (
    id              TEXT PRIMARY KEY,
    buyer_pubkey    TEXT NOT NULL,
    order_id        TEXT NOT NULL,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (buyer_pubkey) REFERENCES users(pubkey),
    FOREIGN KEY (order_id) REFERENCES orders(id)
);

CREATE INDEX IF NOT EXISTS idx_violations_buyer ON freeze_violations(buyer_pubkey);
CREATE INDEX IF NOT EXISTS idx_violations_created ON freeze_violations(created_at);
`

const createBansTable = `
CREATE TABLE IF NOT EXISTS bans (
    pubkey      TEXT PRIMARY KEY,
    violations  INTEGER NOT NULL,
    ban_until   DATETIME NOT NULL,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (pubkey) REFERENCES users(pubkey)
);

CREATE INDEX IF NOT EXISTS idx_bans_until ON bans(ban_until);
`

const createMarketConfigTable = `
CREATE TABLE IF NOT EXISTS market_config (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
`
