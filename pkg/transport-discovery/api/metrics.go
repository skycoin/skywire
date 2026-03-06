// Package api pkg/transport-discovery/metrics.go
package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire/pkg/transport-discovery/store"
)

// parseMetricsQuery extracts common query parameters for metrics endpoints.
func parseMetricsQuery(r *http.Request) store.MetricsQuery {
	query := r.URL.Query()

	q := store.MetricsQuery{
		Days:      0, // default: all available
		Type:      query.Get("type"),
		Live:      query.Get("live"),
		Edges:     query.Get("edges") == "true",
		Bandwidth: true, // default: include bandwidth
		Latency:   true, // default: include latency
	}

	// Parse days
	if d := query.Get("days"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil {
			q.Days = parsed
		}
	}

	// Parse bandwidth (default true)
	if bw := query.Get("bandwidth"); bw == "false" {
		q.Bandwidth = false
	}

	// Parse latency (default true)
	if lat := query.Get("latency"); lat == "false" {
		q.Latency = false
	}

	// Default live to "all"
	if q.Live == "" {
		q.Live = "all"
	}

	return q
}

// parsePKs parses comma-separated public keys from a path parameter.
func parsePKs(pksParam string) ([]cipher.PubKey, error) {
	parts := strings.Split(pksParam, ",")
	pks := make([]cipher.PubKey, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		pk := cipher.PubKey{}
		if err := pk.UnmarshalText([]byte(part)); err != nil {
			return nil, err
		}
		pks = append(pks, pk)
	}

	return pks, nil
}

// parseIDs parses comma-separated UUIDs from a path parameter.
func parseIDs(idsParam string) ([]uuid.UUID, error) {
	parts := strings.Split(idsParam, ",")
	ids := make([]uuid.UUID, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := uuid.Parse(part)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}

	return ids, nil
}

// GET /metric
// Returns network-wide aggregate statistics.
func (api *API) getNetworkMetric(w http.ResponseWriter, r *http.Request) {
	query := parseMetricsQuery(r)

	metrics, err := api.store.GetNetworkMetrics(r.Context(), query)
	if err != nil {
		api.writeError(w, r, err)
		return
	}

	httputil.WriteJSON(w, r, http.StatusOK, metrics)
}

// GET /metric/visor/{pks}
// Returns aggregate metrics for one or more visors (comma-separated PKs).
func (api *API) getVisorAggregateMetric(w http.ResponseWriter, r *http.Request) {
	pksParam := chi.URLParam(r, "pks")

	pks, err := parsePKs(pksParam)
	if err != nil {
		api.log(r).WithError(err).Error("Error parsing PKs")
		api.writeError(w, r, ErrInvalidPubKey)
		return
	}

	if len(pks) == 0 {
		api.writeError(w, r, ErrEmptyPubKey)
		return
	}

	query := parseMetricsQuery(r)

	metrics, err := api.store.GetVisorAggregateMetrics(r.Context(), pks, query)
	if err != nil {
		api.writeError(w, r, err)
		return
	}

	httputil.WriteJSON(w, r, http.StatusOK, metrics)
}

// GET /metrics
// Returns per-transport metrics for all transports.
func (api *API) getAllTransportMetrics(w http.ResponseWriter, r *http.Request) {
	query := parseMetricsQuery(r)

	metrics, err := api.store.GetAllTransportMetrics(r.Context(), query)
	if err != nil {
		api.writeError(w, r, err)
		return
	}

	httputil.WriteJSON(w, r, http.StatusOK, metrics)
}

// GET /metrics/{ids}
// Returns metrics for specific transport(s) (comma-separated IDs).
func (api *API) getTransportMetricsByIDs(w http.ResponseWriter, r *http.Request) {
	idsParam := chi.URLParam(r, "ids")

	ids, err := parseIDs(idsParam)
	if err != nil {
		api.log(r).WithError(err).Error("Error parsing transport IDs")
		api.writeError(w, r, ErrInvalidTransportID)
		return
	}

	if len(ids) == 0 {
		api.writeError(w, r, ErrEmptyTransportID)
		return
	}

	query := parseMetricsQuery(r)

	metrics, err := api.store.GetTransportMetricsByIDs(r.Context(), ids, query)
	if err != nil {
		api.writeError(w, r, err)
		return
	}

	httputil.WriteJSON(w, r, http.StatusOK, metrics)
}

// GET /metrics/visor/{pks}
// Returns per-transport metrics for transports of specified visor(s) (comma-separated PKs).
func (api *API) getTransportMetricsByVisors(w http.ResponseWriter, r *http.Request) {
	pksParam := chi.URLParam(r, "pks")

	pks, err := parsePKs(pksParam)
	if err != nil {
		api.log(r).WithError(err).Error("Error parsing PKs")
		api.writeError(w, r, ErrInvalidPubKey)
		return
	}

	if len(pks) == 0 {
		api.writeError(w, r, ErrEmptyPubKey)
		return
	}

	query := parseMetricsQuery(r)

	metrics, err := api.store.GetTransportMetricsByVisors(r.Context(), pks, query)
	if err != nil {
		api.writeError(w, r, err)
		return
	}

	httputil.WriteJSON(w, r, http.StatusOK, metrics)
}
