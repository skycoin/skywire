package chain

import (
	"net/http"
	"time"
)

// Config holds the SKY-node settings, assembled from the market's operator
// configuration. External-coin explorer config is read per-currency via an
// ExplorerConfigStore (the DB), not from here.
type Config struct {
	SkyNodeURL    string // sky_fullnode_url
	SkySeed       string // sky_wallet_seed (escrow hot wallet seed; spends are signed locally)
	SkyWallet     string // wallet_sky (escrow address; cross-checked against the seed)
	Confirmations int    // required confirmation depth (design: 2)
}

// Chain composes the Skycoin node with a per-currency explorer router to satisfy
// jobs.Chain: SKY deposits/spends go to the node; external-coin payment
// verification is dispatched to whichever explorer the operator configured.
type Chain struct {
	sky *SkyNode
	exp Explorer
}

// New builds a Chain from cfg. store provides per-currency explorer config; a nil
// store disables external payment verification (noExplorer).
func New(cfg Config, store ExplorerConfigStore) *Chain {
	hc := &http.Client{Timeout: 20 * time.Second}
	var exp Explorer = noExplorer{}
	if store != nil {
		exp = newRouter(store, hc)
	}
	return &Chain{
		sky: NewSkyNode(cfg.SkyNodeURL, cfg.SkySeed, cfg.SkyWallet, cfg.Confirmations, hc),
		exp: exp,
	}
}

// DepositConfirmed implements jobs.Chain.
func (c *Chain) DepositConfirmed(marketWallet, senderAddr string, amountSKY float64, notBefore, notAfter time.Time) (bool, string, error) {
	return c.sky.DepositConfirmed(marketWallet, senderAddr, amountSKY, notBefore, notAfter)
}

// PaymentConfirmations implements jobs.Chain.
func (c *Chain) PaymentConfirmations(currency, addr, senderAddr string, expectedAmount float64, notBefore, notAfter time.Time) (int, string, error) {
	return c.exp.PaymentConfirmations(currency, addr, senderAddr, expectedAmount, notBefore, notAfter)
}

// SendSKY implements jobs.Chain.
func (c *Chain) SendSKY(toAddr string, amountSKY float64) (string, error) {
	return c.sky.SendSKY(toAddr, amountSKY)
}

// EscrowBalance implements jobs.Chain: the live spendable SKY balance of the
// escrow wallet, for the escrow-audit consistency check.
func (c *Chain) EscrowBalance(addr string) (float64, error) {
	b, err := c.sky.Balance(addr)
	if err != nil {
		return 0, err
	}
	return b.CoinsSKY, nil
}
