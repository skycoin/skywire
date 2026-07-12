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
	SkyWalletID   string // sky_wallet_id
	SkyWalletPass string // sky_wallet_password
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
		sky: NewSkyNode(cfg.SkyNodeURL, cfg.SkyWalletID, cfg.SkyWalletPass, cfg.Confirmations, hc),
		exp: exp,
	}
}

// DepositConfirmed implements jobs.Chain.
func (c *Chain) DepositConfirmed(marketWallet string, amountSKY float64) (bool, string, error) {
	return c.sky.DepositConfirmed(marketWallet, amountSKY)
}

// PaymentConfirmations implements jobs.Chain.
func (c *Chain) PaymentConfirmations(currency, addr string, expectedAmount float64) (int, string, error) {
	return c.exp.PaymentConfirmations(currency, addr, expectedAmount)
}

// SendSKY implements jobs.Chain.
func (c *Chain) SendSKY(toAddr string, amountSKY float64) (string, error) {
	return c.sky.SendSKY(toAddr, amountSKY)
}
