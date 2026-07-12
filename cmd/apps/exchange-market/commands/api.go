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
	"net/http"

	"github.com/skycoin/skywire/internal/exchange-market/app"
	"github.com/skycoin/skywire/internal/exchange-market/db"
)

// editableConfigKeys is the whitelist of market_config keys the operator UI may
// change. Anything else is rejected so the UI can't write arbitrary rows.
var editableConfigKeys = map[string]bool{
	"wallet_sky":              true,
	"sky_fullnode_url":        true,
	"explorer_btc":            true,
	"explorer_bch":            true,
	"explorer_ltc":            true,
	"explorer_usdt_erc20":     true,
	"explorer_usdt_trc20":     true,
	"fee_rate_sch_per_sky":    true,
	"freeze_violations_limit": true,
	"ban_duration_days":       true,
	"listing_expiry_minutes":  true,
	"order_expiry_minutes":    true,
	"return_delay_hours":      true,
	"cleanup_days":            true,
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
			for k := range updates {
				if !editableConfigKeys[k] {
					mWriteError(w, http.StatusBadRequest, "unknown or read-only config key: "+k)
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
