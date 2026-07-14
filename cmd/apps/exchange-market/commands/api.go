// Package commands cmd/apps/exchange-market/commands/api.go
//
// Operator API consumed by the market's local dashboard. It exposes the
// market's configuration (explorers, SKY fullnode, escrow wallet, ban rules)
// for reading and editing, plus read-only monitoring of products, orders and
// active bans. It is a localhost operator surface, not part of the client<->
// market dmsg protocol.
package commands

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/skycoin/skywire/internal/exchange-market/app"
	"github.com/skycoin/skywire/internal/exchange-market/chain"
	"github.com/skycoin/skywire/internal/exchange-market/db"
	"github.com/skycoin/skywire/internal/exchange-market/protocol"
)

// editableConfigKeys is the whitelist of fixed market_config keys the operator
// UI may change. Per-coin explorer keys (explorer_<coin>_{provider,url,key}) are
// validated dynamically in validateConfigKey.
var editableConfigKeys = map[string]bool{
	"wallet_sky":              true,
	"sky_fullnode_url":        true,
	"sky_wallet_seed":         true,
	"commission_rate_percent": true,
	"commission_min_sky":      true,
	"commission_max_sky":      true,
	"freeze_violations_limit": true,
	"ban_duration_days":       true,
	"listing_expiry_minutes":  true,
	"order_expiry_minutes":    true,
	"cleanup_days":            true,
}

// validateConfigKey allows a fixed whitelisted key, or a per-coin explorer key
// for a supported currency. For a provider assignment it also checks the chosen
// provider actually covers that coin.
func validateConfigKey(key, value string) error {
	if editableConfigKeys[key] {
		return nil
	}
	coin, field, ok := parseExplorerKey(key)
	if !ok || !protocol.IsSupportedCurrency(coin) {
		return fmt.Errorf("unknown or read-only config key: %s", key)
	}
	if field == "provider" && strings.TrimSpace(value) != "" && !chain.SupportsProvider(coin, value) {
		return fmt.Errorf("explorer provider %q does not support %s", value, coin)
	}
	return nil
}

// parseExplorerKey splits "explorer_<coin>_<field>" into (COIN, field). field is
// the last "_"-segment; the coin is everything between.
func parseExplorerKey(key string) (coin, field string, ok bool) {
	rest, found := strings.CutPrefix(key, "explorer_")
	if !found {
		return "", "", false
	}
	i := strings.LastIndex(rest, "_")
	if i <= 0 {
		return "", "", false
	}
	field = rest[i+1:]
	if field != "provider" && field != "url" && field != "key" {
		return "", "", false
	}
	return strings.ToUpper(rest[:i]), field, true
}

// registerOperatorAPI mounts the operator API routes on mux.
func registerOperatorAPI(mux *http.ServeMux, database *db.Database, appCl *app.Client) {
	// GET /api/info — market identity (its visor public key) for clients to use.
	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mWriteError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		mWriteJSON(w, http.StatusOK, map[string]any{"public_key": appCl.VisorPubKey()})
	})

	// GET  /api/config — all market config; POST — update a subset.
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			cfg, err := database.GetAllConfig()
			if err != nil {
				mWriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			mWriteJSON(w, http.StatusOK, cfg)
		case http.MethodPost:
			var updates map[string]string
			if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
				mWriteError(w, http.StatusBadRequest, "invalid config payload")
				return
			}
			for k, v := range updates {
				if err := validateConfigKey(k, v); err != nil {
					mWriteError(w, http.StatusBadRequest, err.Error())
					return
				}
			}
			for k, v := range updates {
				if err := database.SetConfig(k, v); err != nil {
					mWriteError(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
			cfg, err := database.GetAllConfig()
			if err != nil {
				mWriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			mWriteJSON(w, http.StatusOK, cfg)
		default:
			mWriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	// GET /api/products — all products with status.
	mux.HandleFunc("/api/products", func(w http.ResponseWriter, r *http.Request) {
		products, err := database.GetAllProducts()
		if err != nil {
			mWriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		mWriteJSON(w, http.StatusOK, map[string]any{"products": products})
	})

	// GET /api/orders — all orders.
	mux.HandleFunc("/api/orders", func(w http.ResponseWriter, r *http.Request) {
		orders, err := database.GetAllOrders()
		if err != nil {
			mWriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		mWriteJSON(w, http.StatusOK, map[string]any{"orders": orders})
	})

	// GET /api/currencies — per supported coin: which explorer providers can
	// verify it, and the operator's current explorer config. Drives the UI rows.
	mux.HandleFunc("/api/currencies", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mWriteError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		type coinCfg struct {
			Code      string   `json:"code"`
			Providers []string `json:"providers"`
			Provider  string   `json:"provider"`
			URL       string   `json:"url"`
			Key       string   `json:"key"`
			Available bool     `json:"available"`
		}
		out := make([]coinCfg, 0, len(protocol.PaymentCurrencies))
		for _, c := range protocol.PaymentCurrencies {
			provider, url, key, err := database.ExplorerConfig(c)
			if err != nil {
				mWriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			providers := chain.ProvidersFor(c)
			if providers == nil {
				providers = []string{}
			}
			out = append(out, coinCfg{
				Code:      c,
				Providers: providers,
				Provider:  provider,
				URL:       url,
				Key:       key,
				Available: strings.TrimSpace(provider) != "",
			})
		}
		mWriteJSON(w, http.StatusOK, map[string]any{"currencies": out})
	})

	// GET /api/bans — currently active (temporary) bans.
	mux.HandleFunc("/api/bans", func(w http.ResponseWriter, r *http.Request) {
		bans, err := database.GetActiveBans()
		if err != nil {
			mWriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		mWriteJSON(w, http.StatusOK, map[string]any{"bans": bans})
	})
}

func mWriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func mWriteError(w http.ResponseWriter, status int, msg string) {
	mWriteJSON(w, status, map[string]any{"error": msg})
}
