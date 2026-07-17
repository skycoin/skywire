// Package coins is the visor-side coin registry for the embedded skycoin-web
// wallet. skycoin-web is already multicoin: its coin.service fetches the coin
// list from GET /api/v1/coins and its api.service routes each coin's requests to
// that coin's NodeURL. The visor serves this registry at /api/v1/coins with each
// coin's NodeURL set to a "/coin/<index>" prefix, then proxies /coin/<index>/…
// to the matching backend (the skycoin node over dmsg, or the in-visor BTC
// electrum gateway). The wallet source needs no changes.
//
// See docs/design/skycoin-web-multicoin-wallets.md.
package coins

import "encoding/json"

// Coin mirrors the server-data shape skycoin-web's BaseCoin.fromServerData reads
// (coins/basecoin.ts). Only the fields the wallet consumes are set; the rest
// fall back to skycoin-web's own defaults.
type Coin struct {
	ID                int    `json:"id"`
	NodeURL           string `json:"nodeUrl"`
	CoinName          string `json:"coinName"`
	CoinSymbol        string `json:"coinSymbol"`
	HoursName         string `json:"hoursName,omitempty"`
	PriceTickerID     string `json:"priceTickerId,omitempty"`
	PriceTickerSource string `json:"priceTickerSource,omitempty"`
	CoinExplorer      string `json:"coinExplorer,omitempty"`
	CoinType          string `json:"coinType"`
	ServerWallets     bool   `json:"serverWallets"`
}

// IsBitcoin reports whether the coin is routed to the BTC electrum gateway
// rather than a skycoin-style node. Mirrors BaseCoin.isBitcoin().
func (c Coin) IsBitcoin() bool {
	return c.CoinType == "bitcoin" || c.CoinType == "bitcoin-segwit"
}

// Registry is the fixed set of coins the visor serves at /api/v1/coins. The
// slice index equals Coin.ID equals the "/coin/<index>" proxy prefix. Making
// this operator-configurable is a later pass (see the design doc); for now the
// browser holds all keys and does all signing, so ServerWallets is always false.
var Registry = []Coin{
	{
		ID:                0,
		NodeURL:           "/coin/0",
		CoinName:          "Skycoin",
		CoinSymbol:        "SKY",
		HoursName:         "Coin Hours",
		PriceTickerID:     "sky-skycoin",
		PriceTickerSource: "coinpaprika",
		CoinExplorer:      "https://explorer.skycoin.com",
		CoinType:          "skycoin",
		ServerWallets:     false,
	},
	{
		ID:                1,
		NodeURL:           "/coin/1",
		CoinName:          "Bitcoin",
		CoinSymbol:        "BTC",
		PriceTickerID:     "btc-bitcoin",
		PriceTickerSource: "coinpaprika",
		CoinExplorer:      "https://blockstream.info",
		CoinType:          "bitcoin",
		ServerWallets:     false,
	},
}

// ByIndex returns the coin at the given /coin/<index> position.
func ByIndex(index int) (Coin, bool) {
	if index >= 0 && index < len(Registry) {
		return Registry[index], true
	}
	return Coin{}, false
}

// JSON is the marshaled registry served at GET /api/v1/coins.
func JSON() []byte {
	b, err := json.Marshal(Registry)
	if err != nil { // Registry is a static literal; marshal cannot fail.
		return []byte("[]")
	}
	return b
}
