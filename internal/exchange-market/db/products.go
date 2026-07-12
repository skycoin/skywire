// Package db internal/db/products.go
package db

import (
	"database/sql"
	"fmt"
	"time"
)

// CreateProduct creates a new active product (sell order).
func (d *Database) CreateProduct(product *Product) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	product.CreatedAt = time.Now().UTC()

	_, err := d.db.Exec(`
		INSERT INTO products (id, seller_pubkey, amount_sky, price, payment_currency, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, product.ID, product.SellerPubKey, product.AmountSKY, product.Price,
		product.PaymentCurrency, product.Status, product.CreatedAt)

	if err != nil {
		return fmt.Errorf("failed to create product: %w", err)
	}

	return nil
}

// GetProduct retrieves a product by ID.
func (d *Database) GetProduct(id string) (*Product, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	product := &Product{}
	err := d.db.QueryRow(`
		SELECT id, seller_pubkey, amount_sky, price, payment_currency, status, created_at, frozen_at, COALESCE(frozen_by, '') AS frozen_by, sold_at
		FROM products
		WHERE id = ?
	`, id).Scan(&product.ID, &product.SellerPubKey, &product.AmountSKY, &product.Price,
		&product.PaymentCurrency, &product.Status, &product.CreatedAt,
		&product.FrozenAt, &product.FrozenBy, &product.SoldAt)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get product: %w", err)
	}

	return product, nil
}

// GetActiveProducts returns all active products available for purchase.
func (d *Database) GetActiveProducts() ([]*Product, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.Query(`
		SELECT id, seller_pubkey, amount_sky, price, payment_currency, status, created_at, frozen_at, COALESCE(frozen_by, '') AS frozen_by, sold_at
		FROM products
		WHERE status = 'active'
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to get active products: %w", err)
	}
	defer rows.Close() //nolint

	var products []*Product
	for rows.Next() {
		product := &Product{}
		err := rows.Scan(&product.ID, &product.SellerPubKey, &product.AmountSKY, &product.Price,
			&product.PaymentCurrency, &product.Status, &product.CreatedAt,
			&product.FrozenAt, &product.FrozenBy, &product.SoldAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan product: %w", err)
		}
		products = append(products, product)
	}

	return products, nil
}

// FreezeProduct marks a product as frozen by a buyer.
func (d *Database) FreezeProduct(productID, buyerPubKey string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now().UTC()
	result, err := d.db.Exec(`
		UPDATE products
		SET status = 'frozen', frozen_at = ?, frozen_by = ?
		WHERE id = ? AND status = 'active'
	`, now, buyerPubKey, productID)

	if err != nil {
		return fmt.Errorf("failed to freeze product: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("product not available for freezing: %s", productID)
	}

	return nil
}

// UnfreezeProduct returns a frozen product back to active status.
func (d *Database) UnfreezeProduct(productID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.db.Exec(`
		UPDATE products
		SET status = 'active', frozen_at = NULL, frozen_by = NULL
		WHERE id = ? AND status = 'frozen'
	`, productID)

	if err != nil {
		return fmt.Errorf("failed to unfreeze product: %w", err)
	}

	return nil
}

// MarkProductSold marks a product as sold.
func (d *Database) MarkProductSold(productID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now().UTC()
	_, err := d.db.Exec(`
		UPDATE products
		SET status = 'sold', sold_at = ?
		WHERE id = ?
	`, now, productID)

	if err != nil {
		return fmt.Errorf("failed to mark product as sold: %w", err)
	}

	return nil
}

// MarkProductCancelled marks a product as canceled (seller withdrew or expired).
func (d *Database) MarkProductCancelled(productID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.db.Exec(`
		UPDATE products
		SET status = 'cancelled'
		WHERE id = ?
	`, productID)

	if err != nil {
		return fmt.Errorf("failed to mark product as canceled: %w", err)
	}

	return nil
}
