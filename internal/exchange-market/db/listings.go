// Package db internal/db/listings.go
package db

import (
	"database/sql"
	"fmt"
	"time"
)

// CreatePendingListing creates a new pending listing (sell order awaiting SKY transfer).
func (d *Database) CreatePendingListing(listing *PendingListing) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	listing.CreatedAt = time.Now().UTC()

	_, err := d.db.Exec(`
		INSERT INTO pending_listings (id, seller_pubkey, amount_sky, expected_amount_sky, price, payment_currency, status, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, listing.ID, listing.SellerPubKey, listing.AmountSKY, listing.ExpectedAmountSKY,
		listing.Price, listing.PaymentCurrency, listing.Status, listing.ExpiresAt, listing.CreatedAt)

	if err != nil {
		return fmt.Errorf("failed to create pending listing: %w", err)
	}

	return nil
}

// GetPendingListing retrieves a pending listing by ID.
func (d *Database) GetPendingListing(id string) (*PendingListing, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	listing := &PendingListing{}
	err := d.db.QueryRow(`
		SELECT id, seller_pubkey, amount_sky, expected_amount_sky, price, payment_currency, status, expires_at, created_at, confirmed_at, COALESCE(tx_hash, '') AS tx_hash
		FROM pending_listings
		WHERE id = ?
	`, id).Scan(&listing.ID, &listing.SellerPubKey, &listing.AmountSKY, &listing.ExpectedAmountSKY,
		&listing.Price, &listing.PaymentCurrency, &listing.Status, &listing.ExpiresAt,
		&listing.CreatedAt, &listing.ConfirmedAt, &listing.TxHash)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get pending listing: %w", err)
	}

	return listing, nil
}

// UpdatePendingListingStatus updates the status of a pending listing.
func (d *Database) UpdatePendingListingStatus(id, status string, txHash string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	var confirmedAt *time.Time
	if status == "confirmed" {
		now := time.Now().UTC()
		confirmedAt = &now
	}

	_, err := d.db.Exec(`
		UPDATE pending_listings
		SET status = ?, confirmed_at = ?, tx_hash = ?
		WHERE id = ?
	`, status, confirmedAt, txHash, id)

	if err != nil {
		return fmt.Errorf("failed to update pending listing status: %w", err)
	}

	return nil
}

// GetExpiredPendingListings returns all pending listings that have expired.
func (d *Database) GetExpiredPendingListings() ([]*PendingListing, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	now := time.Now().UTC()
	rows, err := d.db.Query(`
		SELECT id, seller_pubkey, amount_sky, expected_amount_sky, price, payment_currency, status, expires_at, created_at, confirmed_at, COALESCE(tx_hash, '') AS tx_hash
		FROM pending_listings
		WHERE status = 'pending' AND expires_at <= ?
	`, now)
	if err != nil {
		return nil, fmt.Errorf("failed to get expired pending listings: %w", err)
	}
	defer rows.Close() //nolint

	var listings []*PendingListing
	for rows.Next() {
		listing := &PendingListing{}
		err := rows.Scan(&listing.ID, &listing.SellerPubKey, &listing.AmountSKY, &listing.ExpectedAmountSKY,
			&listing.Price, &listing.PaymentCurrency, &listing.Status, &listing.ExpiresAt,
			&listing.CreatedAt, &listing.ConfirmedAt, &listing.TxHash)
		if err != nil {
			return nil, fmt.Errorf("failed to scan pending listing: %w", err)
		}
		listings = append(listings, listing)
	}

	return listings, nil
}

// GetSellerPendingListings returns all pending listings for a specific seller.
func (d *Database) GetSellerPendingListings(sellerPubKey string) ([]*PendingListing, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.Query(`
		SELECT id, seller_pubkey, amount_sky, expected_amount_sky, price, payment_currency, status, expires_at, created_at, confirmed_at, COALESCE(tx_hash, '') AS tx_hash
		FROM pending_listings
		WHERE seller_pubkey = ? AND status IN ('pending', 'confirmed')
		ORDER BY created_at DESC
	`, sellerPubKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get seller pending listings: %w", err)
	}
	defer rows.Close() //nolint

	var listings []*PendingListing
	for rows.Next() {
		listing := &PendingListing{}
		err := rows.Scan(&listing.ID, &listing.SellerPubKey, &listing.AmountSKY, &listing.ExpectedAmountSKY,
			&listing.Price, &listing.PaymentCurrency, &listing.Status, &listing.ExpiresAt,
			&listing.CreatedAt, &listing.ConfirmedAt, &listing.TxHash)
		if err != nil {
			return nil, fmt.Errorf("failed to scan pending listing: %w", err)
		}
		listings = append(listings, listing)
	}

	return listings, nil
}

// GetPendingListings returns pending listings that have not yet expired. The
// Listing Checker polls these to detect the seller's SKY deposit.
func (d *Database) GetPendingListings() ([]*PendingListing, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	now := time.Now().UTC()
	rows, err := d.db.Query(`
		SELECT id, seller_pubkey, amount_sky, expected_amount_sky, price, payment_currency, status, expires_at, created_at, confirmed_at, COALESCE(tx_hash, '') AS tx_hash
		FROM pending_listings
		WHERE status = 'pending' AND expires_at > ?
		ORDER BY created_at ASC
	`, now)
	if err != nil {
		return nil, fmt.Errorf("failed to get pending listings: %w", err)
	}
	defer rows.Close() //nolint

	var listings []*PendingListing
	for rows.Next() {
		listing := &PendingListing{}
		if err := rows.Scan(&listing.ID, &listing.SellerPubKey, &listing.AmountSKY, &listing.ExpectedAmountSKY,
			&listing.Price, &listing.PaymentCurrency, &listing.Status, &listing.ExpiresAt,
			&listing.CreatedAt, &listing.ConfirmedAt, &listing.TxHash); err != nil {
			return nil, fmt.Errorf("failed to scan pending listing: %w", err)
		}
		listings = append(listings, listing)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate pending listings: %w", err)
	}

	return listings, nil
}

// DeleteOldPendingListings removes terminal (expired/confirmed/canceled)
// pending listings older than age. Returns the number deleted.
func (d *Database) DeleteOldPendingListings(age time.Duration) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	cutoff := time.Now().UTC().Add(-age)
	res, err := d.db.Exec(`
		DELETE FROM pending_listings
		WHERE status IN ('expired', 'confirmed', 'cancelled') AND created_at < ?
	`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("failed to delete old pending listings: %w", err)
	}
	n, _ := res.RowsAffected() //nolint:errcheck
	return n, nil
}

// PendingListingAmountExists reports whether a still-pending listing already
// expects a SKY deposit within tol of amount. Used to keep every pending
// deposit's non-round amount unique to the (single) market wallet.
func (d *Database) PendingListingAmountExists(amount, tol float64) (bool, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var n int
	err := d.db.QueryRow(`
		SELECT COUNT(*) FROM pending_listings
		WHERE status = 'pending' AND ABS(expected_amount_sky - ?) < ?
	`, amount, tol).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("failed to check pending listing amount: %w", err)
	}
	return n > 0, nil
}
