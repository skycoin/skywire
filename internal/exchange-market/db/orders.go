// Package db internal/db/orders.go
package db

import (
	"database/sql"
	"fmt"
	"time"
)

// CreateOrder creates a new buy order.
func (d *Database) CreateOrder(order *Order) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	order.CreatedAt = time.Now().UTC()

	_, err := d.db.Exec(`
		INSERT INTO orders (id, product_id, buyer_pubkey, amount_sky, price, payment_currency, expected_payment_amount, seller_wallet, status, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, order.ID, order.ProductID, order.BuyerPubKey, order.AmountSKY, order.Price,
		order.PaymentCurrency, order.ExpectedPaymentAmount, order.SellerWallet,
		order.Status, order.ExpiresAt, order.CreatedAt)

	if err != nil {
		return fmt.Errorf("failed to create order: %w", err)
	}

	return nil
}

// GetOrder retrieves an order by ID.
func (d *Database) GetOrder(id string) (*Order, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	order := &Order{}
	err := d.db.QueryRow(`
		SELECT id, product_id, buyer_pubkey, amount_sky, price, payment_currency, expected_payment_amount, seller_wallet, status, expires_at, created_at, paid_at, payment_tx_hash, confirmations, completed_at
		FROM orders
		WHERE id = ?
	`, id).Scan(&order.ID, &order.ProductID, &order.BuyerPubKey, &order.AmountSKY, &order.Price,
		&order.PaymentCurrency, &order.ExpectedPaymentAmount, &order.SellerWallet,
		&order.Status, &order.ExpiresAt, &order.CreatedAt, &order.PaidAt,
		&order.PaymentTxHash, &order.Confirmations, &order.CompletedAt)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get order: %w", err)
	}

	return order, nil
}

// GetBuyerOrders returns all orders for a specific buyer.
func (d *Database) GetBuyerOrders(buyerPubKey string) ([]*Order, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.Query(`
		SELECT id, product_id, buyer_pubkey, amount_sky, price, payment_currency, expected_payment_amount, seller_wallet, status, expires_at, created_at, paid_at, payment_tx_hash, confirmations, completed_at
		FROM orders
		WHERE buyer_pubkey = ?
		ORDER BY created_at DESC
	`, buyerPubKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get buyer orders: %w", err)
	}
	defer rows.Close()

	var orders []*Order
	for rows.Next() {
		order := &Order{}
		err := rows.Scan(&order.ID, &order.ProductID, &order.BuyerPubKey, &order.AmountSKY, &order.Price,
			&order.PaymentCurrency, &order.ExpectedPaymentAmount, &order.SellerWallet,
			&order.Status, &order.ExpiresAt, &order.CreatedAt, &order.PaidAt,
			&order.PaymentTxHash, &order.Confirmations, &order.CompletedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan order: %w", err)
		}
		orders = append(orders, order)
	}

	return orders, nil
}

// GetSellerOrders returns all orders where the seller matches the given pubkey.
func (d *Database) GetSellerOrders(sellerPubKey string) ([]*Order, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.Query(`
		SELECT o.id, o.product_id, o.buyer_pubkey, o.amount_sky, o.price, o.payment_currency, o.expected_payment_amount, o.seller_wallet, o.status, o.expires_at, o.created_at, o.paid_at, o.payment_tx_hash, o.confirmations, o.completed_at
		FROM orders o
		INNER JOIN products p ON o.product_id = p.id
		WHERE p.seller_pubkey = ?
		ORDER BY o.created_at DESC
	`, sellerPubKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get seller orders: %w", err)
	}
	defer rows.Close()

	var orders []*Order
	for rows.Next() {
		order := &Order{}
		err := rows.Scan(&order.ID, &order.ProductID, &order.BuyerPubKey, &order.AmountSKY, &order.Price,
			&order.PaymentCurrency, &order.ExpectedPaymentAmount, &order.SellerWallet,
			&order.Status, &order.ExpiresAt, &order.CreatedAt, &order.PaidAt,
			&order.PaymentTxHash, &order.Confirmations, &order.CompletedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan order: %w", err)
		}
		orders = append(orders, order)
	}

	return orders, nil
}

// UpdateOrderStatus updates the status of an order.
func (d *Database) UpdateOrderStatus(id, status string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.db.Exec(`
		UPDATE orders
		SET status = ?
		WHERE id = ?
	`, status, id)

	if err != nil {
		return fmt.Errorf("failed to update order status: %w", err)
	}

	return nil
}

// MarkOrderPaid marks an order as paid with the transaction hash.
func (d *Database) MarkOrderPaid(orderID, txHash string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now().UTC()
	_, err := d.db.Exec(`
		UPDATE orders
		SET status = 'paid', paid_at = ?, payment_tx_hash = ?
		WHERE id = ?
	`, now, txHash, orderID)

	if err != nil {
		return fmt.Errorf("failed to mark order as paid: %w", err)
	}

	return nil
}

// IncrementOrderConfirmations increments the confirmation count for an order.
func (d *Database) IncrementOrderConfirmations(orderID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.db.Exec(`
		UPDATE orders
		SET confirmations = confirmations + 1
		WHERE id = ?
	`, orderID)

	if err != nil {
		return fmt.Errorf("failed to increment order confirmations: %w", err)
	}

	return nil
}

// MarkOrderConfirmed marks an order as confirmed (2+ confirmations received).
func (d *Database) MarkOrderConfirmed(orderID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.db.Exec(`
		UPDATE orders
		SET status = 'confirmed'
		WHERE id = ?
	`, orderID)

	if err != nil {
		return fmt.Errorf("failed to mark order as confirmed: %w", err)
	}

	return nil
}

// MarkOrderCompleted marks an order as completed (SKY transferred to buyer).
func (d *Database) MarkOrderCompleted(orderID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now().UTC()
	_, err := d.db.Exec(`
		UPDATE orders
		SET status = 'completed', completed_at = ?
		WHERE id = ?
	`, now, orderID)

	if err != nil {
		return fmt.Errorf("failed to mark order as completed: %w", err)
	}

	return nil
}

// MarkOrderExpired marks an order as expired.
func (d *Database) MarkOrderExpired(orderID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.db.Exec(`
		UPDATE orders
		SET status = 'expired'
		WHERE id = ?
	`, orderID)

	if err != nil {
		return fmt.Errorf("failed to mark order as expired: %w", err)
	}

	return nil
}

// GetExpiredOrders returns all orders that have expired (pending_payment status + past expires_at).
func (d *Database) GetExpiredOrders() ([]*Order, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	now := time.Now().UTC()
	rows, err := d.db.Query(`
		SELECT id, product_id, buyer_pubkey, amount_sky, price, payment_currency, expected_payment_amount, seller_wallet, status, expires_at, created_at, paid_at, payment_tx_hash, confirmations, completed_at
		FROM orders
		WHERE status = 'pending_payment' AND expires_at <= ?
	`, now)
	if err != nil {
		return nil, fmt.Errorf("failed to get expired orders: %w", err)
	}
	defer rows.Close()

	var orders []*Order
	for rows.Next() {
		order := &Order{}
		err := rows.Scan(&order.ID, &order.ProductID, &order.BuyerPubKey, &order.AmountSKY, &order.Price,
			&order.PaymentCurrency, &order.ExpectedPaymentAmount, &order.SellerWallet,
			&order.Status, &order.ExpiresAt, &order.CreatedAt, &order.PaidAt,
			&order.PaymentTxHash, &order.Confirmations, &order.CompletedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan order: %w", err)
		}
		orders = append(orders, order)
	}

	return orders, nil
}

// GetPendingPaymentOrders returns all orders waiting for payment confirmation.
func (d *Database) GetPendingPaymentOrders() ([]*Order, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.Query(`
		SELECT id, product_id, buyer_pubkey, amount_sky, price, payment_currency, expected_payment_amount, seller_wallet, status, expires_at, created_at, paid_at, payment_tx_hash, confirmations, completed_at
		FROM orders
		WHERE status = 'pending_payment' AND expires_at > ?
		ORDER BY created_at ASC
	`, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("failed to get pending payment orders: %w", err)
	}
	defer rows.Close()

	var orders []*Order
	for rows.Next() {
		order := &Order{}
		err := rows.Scan(&order.ID, &order.ProductID, &order.BuyerPubKey, &order.AmountSKY, &order.Price,
			&order.PaymentCurrency, &order.ExpectedPaymentAmount, &order.SellerWallet,
			&order.Status, &order.ExpiresAt, &order.CreatedAt, &order.PaidAt,
			&order.PaymentTxHash, &order.Confirmations, &order.CompletedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan order: %w", err)
		}
		orders = append(orders, order)
	}

	return orders, nil
}

// GetCompletedOrdersOlderThan returns all completed orders older than the specified duration.
func (d *Database) GetCompletedOrdersOlderThan(age time.Duration) ([]*Order, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	cutoff := time.Now().UTC().Add(-age)
	rows, err := d.db.Query(`
		SELECT id, product_id, buyer_pubkey, amount_sky, price, payment_currency, expected_payment_amount, seller_wallet, status, expires_at, created_at, paid_at, payment_tx_hash, confirmations, completed_at
		FROM orders
		WHERE status = 'completed' AND completed_at <= ?
	`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("failed to get completed orders: %w", err)
	}
	defer rows.Close()

	var orders []*Order
	for rows.Next() {
		order := &Order{}
		err := rows.Scan(&order.ID, &order.ProductID, &order.BuyerPubKey, &order.AmountSKY, &order.Price,
			&order.PaymentCurrency, &order.ExpectedPaymentAmount, &order.SellerWallet,
			&order.Status, &order.ExpiresAt, &order.CreatedAt, &order.PaidAt,
			&order.PaymentTxHash, &order.Confirmations, &order.CompletedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan order: %w", err)
		}
		orders = append(orders, order)
	}

	return orders, nil
}

// DeleteOrder deletes an order by ID.
func (d *Database) DeleteOrder(orderID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.db.Exec(`DELETE FROM orders WHERE id = ?`, orderID)
	if err != nil {
		return fmt.Errorf("failed to delete order: %w", err)
	}

	return nil
}
