// Package jobs implements the exchange-market background jobs (see
// exchange-design.md §7.8): escrow/listing verification, expiry handling,
// cleanup and ban enforcement.
package jobs

import "errors"

// ErrNoChain is returned by NoopChain for any operation that needs a real
// blockchain/explorer backend.
var ErrNoChain = errors.New("no blockchain backend configured")

// Chain abstracts the blockchain/explorer operations the jobs depend on. A real
// implementation (SKY fullnode + per-coin explorers, using the operator-
// configured URLs) is provided in a later phase; jobs are written against this
// interface so they are testable and the backend is swappable.
type Chain interface {
	// DepositConfirmed reports whether the market wallet has received a SKY
	// deposit of amountSKY with enough confirmations to be considered settled.
	// Used by the Listing Checker to promote a pending listing into a product.
	DepositConfirmed(marketWallet string, amountSKY float64) (confirmed bool, txHash string, err error)

	// PaymentConfirmations returns how many confirmations a payment of
	// expectedAmount in currency to addr currently has (0 if not observed).
	// Used by the Escrow Checker to track a buyer's payment to the seller.
	PaymentConfirmations(currency, addr string, expectedAmount float64) (confirmations int, txHash string, err error)

	// SendSKY transfers amountSKY of SKY from the market wallet to addr and
	// returns the transaction hash. Used to deliver SKY to a buyer on a
	// completed trade and to return escrow to a seller.
	SendSKY(toAddr string, amountSKY float64) (txHash string, err error)
}

// NoopChain is the default Chain used until a real backend is configured. It
// never confirms anything and refuses to send, so the jobs run safely without
// advancing any trade on-chain.
type NoopChain struct{}

// DepositConfirmed always reports not-confirmed.
func (NoopChain) DepositConfirmed(string, float64) (bool, string, error) {
	return false, "", nil
}

// PaymentConfirmations always reports zero confirmations.
func (NoopChain) PaymentConfirmations(string, string, float64) (int, string, error) {
	return 0, "", nil
}

// SendSKY always fails: there is no backend to send from.
func (NoopChain) SendSKY(string, float64) (string, error) {
	return "", ErrNoChain
}
