// Package db internal/db/models.go
package db

import "time"

// User represents a user (buyer or seller) in the exchange.
// Users are identified by their Skywire Visor public key.
type User struct {
	PubKey          string    `json:"pubkey" db:"pubkey"`
	WalletSKY       string    `json:"wallet_sky" db:"wallet_sky"`
	WalletBTC       string    `json:"wallet_btc,omitempty" db:"wallet_btc"`
	WalletBCH       string    `json:"wallet_bch,omitempty" db:"wallet_bch"`
	WalletLTC       string    `json:"wallet_ltc,omitempty" db:"wallet_ltc"`
	WalletUSDT_ERC20 string  `json:"wallet_usdt_erc20,omitempty" db:"wallet_usdt_erc20"`
	WalletUSDT_TRC20 string  `json:"wallet_usdt_trc20,omitempty" db:"wallet_usdt_trc20"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
}

// PendingListing represents a sell order that is waiting for the seller
// to transfer SKY tokens to the market wallet.
type PendingListing struct {
	ID                string     `json:"id" db:"id"`
	SellerPubKey      string     `json:"seller_pubkey" db:"seller_pubkey"`
	AmountSKY         float64    `json:"amount_sky" db:"amount_sky"`
	ExpectedAmountSKY float64    `json:"expected_amount_sky" db:"expected_amount_sky"`
	Price             float64    `json:"price" db:"price"`
	PaymentCurrency   string     `json:"payment_currency" db:"payment_currency"`
	Status            string     `json:"status" db:"status"` // pending, confirmed, expired, cancelled
	ExpiresAt         time.Time  `json:"expires_at" db:"expires_at"`
	CreatedAt         time.Time  `json:"created_at" db:"created_at"`
	ConfirmedAt       *time.Time `json:"confirmed_at,omitempty" db:"confirmed_at"`
	TxHash            string     `json:"tx_hash,omitempty" db:"tx_hash"`
}

// Product represents an active product (sell order) available for purchase.
type Product struct {
	ID              string     `json:"id" db:"id"`
	SellerPubKey    string     `json:"seller_pubkey" db:"seller_pubkey"`
	AmountSKY       float64    `json:"amount_sky" db:"amount_sky"`
	Price           float64    `json:"price" db:"price"`
	PaymentCurrency string     `json:"payment_currency" db:"payment_currency"`
	Status          string     `json:"status" db:"status"` // active, frozen, sold, expired, cancelled
	CreatedAt       time.Time  `json:"created_at" db:"created_at"`
	FrozenAt        *time.Time `json:"frozen_at,omitempty" db:"frozen_at"`
	FrozenBy        string     `json:"frozen_by,omitempty" db:"frozen_by"`
	SoldAt          *time.Time `json:"sold_at,omitempty" db:"sold_at"`
}

// Order represents a buy order created when a buyer selects a product.
type Order struct {
	ID                    string     `json:"id" db:"id"`
	ProductID             string     `json:"product_id" db:"product_id"`
	BuyerPubKey           string     `json:"buyer_pubkey" db:"buyer_pubkey"`
	AmountSKY             float64    `json:"amount_sky" db:"amount_sky"`
	Price                 float64    `json:"price" db:"price"`
	PaymentCurrency       string     `json:"payment_currency" db:"payment_currency"`
	ExpectedPaymentAmount float64    `json:"expected_payment_amount" db:"expected_payment_amount"`
	SellerWallet          string     `json:"seller_wallet" db:"seller_wallet"`
	Status                string     `json:"status" db:"status"` // pending_payment, paid, confirmed, completed, expired, cancelled
	ExpiresAt             time.Time  `json:"expires_at" db:"expires_at"`
	CreatedAt             time.Time  `json:"created_at" db:"created_at"`
	PaidAt                *time.Time `json:"paid_at,omitempty" db:"paid_at"`
	PaymentTxHash         string     `json:"payment_tx_hash,omitempty" db:"payment_tx_hash"`
	Confirmations         int        `json:"confirmations" db:"confirmations"`
	CompletedAt           *time.Time `json:"completed_at,omitempty" db:"completed_at"`
}

// FreezeViolation records a freeze that did not result in a completed purchase.
// Used to track user violations for the ban system.
type FreezeViolation struct {
	ID           string    `json:"id" db:"id"`
	BuyerPubKey  string    `json:"buyer_pubkey" db:"buyer_pubkey"`
	OrderID      string    `json:"order_id" db:"order_id"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
}

// Ban represents a banned user with their ban expiry date.
type Ban struct {
	PubKey     string    `json:"pubkey" db:"pubkey"`
	Violations int       `json:"violations" db:"violations"`
	BanUntil   time.Time `json:"ban_until" db:"ban_until"`
	CreatedAt  time.Time `json:"created_at" db:"created_at"`
}

// MarketConfig stores key-value configuration for the market.
type MarketConfig struct {
	Key       string    `json:"key" db:"key"`
	Value     string    `json:"value" db:"value"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}
