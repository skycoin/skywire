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
		SELECT id, seller_pubkey, amount_sky, expected_amount_sky, price, payment_currency, status, expires_at, created_at, confirmed_at, tx_hash
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
		SELECT id, seller_pubkey, amount_sky, expected_amount_sky, price, payment_currency, status, expires_at, created_at, confirmed_at, tx_hash
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
		SELECT id, seller_pubkey, amount_sky, expected_amount_sky, price, payment_currency, status, expires_at, created_at, confirmed_at, tx_hash
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
