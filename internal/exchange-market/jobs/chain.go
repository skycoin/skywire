// Package jobs implements the exchange-market background jobs (see
// exchange-design.md §7.8): escrow/listing verification, expiry handling,
// cleanup and ban enforcement.
package jobs

import (
	"errors"
	"time"
)

// ErrNoChain is returned by NoopChain for any operation that needs a real
// blockchain/explorer backend.
var ErrNoChain = errors.New("no blockchain backend configured")

// Chain abstracts the blockchain/explorer operations the jobs depend on. A real
// implementation (SKY fullnode + per-coin explorers, using the operator-
// configured URLs) is provided in a later phase; jobs are written against this
// interface so they are testable and the backend is swappable.
type Chain interface {
	// DepositConfirmed reports whether the market wallet received a settled SKY
	// deposit of amountSKY sent FROM senderAddr (the seller's registered SKY
	// address) within [notBefore, notAfter] (the listing's deposit window). The
	// sender + window disambiguate a shared escrow wallet and block replay of an
	// old transaction. Used by the Listing Checker to promote a pending listing.
	DepositConfirmed(marketWallet, senderAddr string, amountSKY float64, notBefore, notAfter time.Time) (confirmed bool, txHash string, err error)

	// PaymentConfirmations returns how many confirmations a payment of
	// expectedAmount in currency to addr currently has (0 if not observed). Only a
	// payment within [notBefore, notAfter] — the order's window — counts (blocks
	// replay of an old payment of a recycled amount); senderAddr, the buyer's
	// registered payment address, is a soft corroborating signal. Used by the
	// Escrow Checker to track a buyer's payment to the seller.
	PaymentConfirmations(currency, addr, senderAddr string, expectedAmount float64, notBefore, notAfter time.Time) (confirmations int, txHash string, err error)

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
func (NoopChain) DepositConfirmed(string, string, float64, time.Time, time.Time) (bool, string, error) {
	return false, "", nil
}

// PaymentConfirmations always reports zero confirmations.
func (NoopChain) PaymentConfirmations(string, string, string, float64, time.Time, time.Time) (int, string, error) {
	return 0, "", nil
}

// SendSKY always fails: there is no backend to send from.
func (NoopChain) SendSKY(string, float64) (string, error) {
	return "", ErrNoChain
}
