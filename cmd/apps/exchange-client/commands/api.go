// Package commands cmd/apps/exchange-client/commands/api.go
//
// Local control API consumed by the embedded trading UI. It exposes the
// configured default market public key and drives the manual connect/disconnect
// to a market over dmsg. The client never connects automatically: the UI shows
// the (pre-filled) market public key and the user clicks Connect.
package commands

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/skycoin/skywire/internal/exchange-client/market"
	"github.com/skycoin/skywire/internal/exchange-market/protocol"
	"github.com/skycoin/skywire/pkg/cipher"
)

// session holds the client's (single) connection to a market. It is safe for
// concurrent use by the HTTP handlers.
type session struct {
	dialer    market.Dialer
	defaultPK string

	mu   sync.Mutex
	conn *market.Conn
	pk   string // public key of the currently connected market ("" if none)
}

func newSession(dialer market.Dialer, defaultPK string) *session {
	return &session{dialer: dialer, defaultPK: strings.TrimSpace(defaultPK)}
}

// connect dials the market at pkHex over dmsg and performs a lightweight
// handshake (get_currencies) to confirm the link. On success it replaces any
// existing connection and returns the market's available payment currencies.
func (s *session) connect(pkHex string) ([]string, error) {
	pkHex = strings.TrimSpace(pkHex)
	if pkHex == "" {
		return nil, errors.New("market public key is required")
	}
	var pk cipher.PubKey
	if err := pk.UnmarshalText([]byte(pkHex)); err != nil {
		return nil, errors.New("invalid market public key")
	}

	conn, err := market.Dial(s.dialer, pk, protocol.DefaultPort)
	if err != nil {
		return nil, err
	}

	resp, err := conn.Do(protocol.TypeGetCurrencies, nil)
	if err != nil {
		_ = conn.Close() //nolint:errcheck
		return nil, err
	}
	if resp.IsError() {
		_ = conn.Close() //nolint:errcheck
		var e protocol.ErrorData
		_ = resp.Bind(&e) //nolint:errcheck
		return nil, errors.New("market rejected connection: " + e.Message)
	}
	var currencies protocol.GetCurrenciesResponse
	_ = resp.Bind(&currencies) //nolint:errcheck

	s.mu.Lock()
	if s.conn != nil {
		_ = s.conn.Close() //nolint:errcheck
	}
	s.conn = conn
	s.pk = pkHex
	s.mu.Unlock()

	return currencies.Currencies, nil
}

// disconnect closes the current market connection, if any.
func (s *session) disconnect() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		_ = s.conn.Close() //nolint:errcheck
		s.conn = nil
		s.pk = ""
	}
}

// close releases resources on shutdown.
func (s *session) close() { s.disconnect() }

func (s *session) status() (connected bool, pk string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn != nil, s.pk
}

// registerAPI mounts the control API routes on mux.
func registerAPI(mux *http.ServeMux, sess *session) {
	// GET /api/config — the default market public key to pre-fill in the UI.
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"market_pk": sess.defaultPK})
	})

	// GET /api/status — current connection state.
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		connected, pk := sess.status()
		writeJSON(w, http.StatusOK, map[string]any{"connected": connected, "market_pk": pk})
	})

	// POST /api/connect {market_pk} — dial the market and confirm the link.
	mux.HandleFunc("/api/connect", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			MarketPK string `json:"market_pk"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
		pk := body.MarketPK
		if strings.TrimSpace(pk) == "" {
			pk = sess.defaultPK
		}
		currencies, err := sess.connect(pk)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"connected":  true,
			"market_pk":  strings.TrimSpace(pk),
			"currencies": currencies,
		})
	})

	// POST /api/disconnect — drop the current connection.
	mux.HandleFunc("/api/disconnect", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		sess.disconnect()
		writeJSON(w, http.StatusOK, map[string]any{"connected": false})
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}
