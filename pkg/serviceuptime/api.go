// Package serviceuptime — pkg/serviceuptime/api.go: HTTP handlers
// that surface the Recorder's history. Exported as plain
// http.HandlerFunc so callers can wrap with chi (services) or gin
// (the visor's logserver) without dragging either framework into
// this package.
package serviceuptime

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// CurrentSessionHandler serves GET /uptime/now → JSON SessionRecord.
// Reads the in-flight session row directly from the recorder, so a
// caller can ask "what version is running right now" without a bbolt
// round-trip.
func CurrentSessionHandler(recorder *Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, recorder.CurrentSession())
	}
}

// SessionsHandler serves GET /uptime/sessions → JSON []SessionRecord.
// Query: since=<RFC3339|unix>, limit=<n> (most-recent N rows).
func SessionsHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var since time.Time
		if s := r.URL.Query().Get("since"); s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				since = t
			} else if u, err := strconv.ParseInt(s, 10, 64); err == nil {
				since = time.Unix(u, 0).UTC()
			} else {
				http.Error(w, "invalid since (want RFC3339 or unix seconds)", http.StatusBadRequest)
				return
			}
		}
		out, err := store.Sessions(since)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n >= 0 && n < len(out) {
				out = out[len(out)-n:]
			}
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// TimelineHandler serves GET /uptime/timeline → 36-byte octet-stream.
// Query: date=YYYY-MM-DD (defaults to today UTC).
func TimelineHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dateStr := r.URL.Query().Get("date")
		if dateStr == "" {
			dateStr = time.Now().UTC().Format(dateFmt)
		}
		date, err := time.Parse(dateFmt, dateStr)
		if err != nil {
			http.Error(w, "invalid date (want YYYY-MM-DD)", http.StatusBadRequest)
			return
		}
		bm, err := store.Bitmap(date)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Uptime-Date", dateStr)
		_, _ = w.Write(bm) //nolint:errcheck
	}
}

// DatesHandler serves GET /uptime/dates → JSON []string.
func DatesHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		dates, err := store.Dates()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, dates)
	}
}

// RegisterRoutes is a chi.Router convenience wrapper that mounts all
// /uptime/* routes on r. Services using chi (TPD, AR, RF, UT) call
// this from their api.New constructor.
func RegisterRoutes(r chi.Router, recorder *Recorder) {
	r.Get("/uptime/now", CurrentSessionHandler(recorder))
	r.Get("/uptime/sessions", SessionsHandler(recorder.Store()))
	r.Get("/uptime/timeline", TimelineHandler(recorder.Store()))
	r.Get("/uptime/dates", DatesHandler(recorder.Store()))
}

// RegisterReadOnlyRoutes mounts the read endpoints minus /uptime/now,
// for callers with a Store but no live Recorder (e.g. an inspector
// tool reading a copied bbolt file).
func RegisterReadOnlyRoutes(r chi.Router, store *Store) {
	r.Get("/uptime/sessions", SessionsHandler(store))
	r.Get("/uptime/timeline", TimelineHandler(store))
	r.Get("/uptime/dates", DatesHandler(store))
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v) //nolint:errcheck
}
