// Package api pkg/transport-discovery/endpoints.go
package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport-discovery/store"
)

func (api *API) registerTransport(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		api.writeError(w, r, err)
		return
	}

	var entries []*transport.SignedEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		api.writeError(w, r, err)
		return
	}

	// Register all transports in a single Redis pipeline (batch).
	// This reduces N separate pipelines to 1, cutting Redis round-trips.
	if err := api.store.RegisterTransportsBatch(r.Context(), entries); err != nil {
		api.writeError(w, r, err)
		return
	}

	// Post-registration: CXO publish, DHT mirror, heartbeats.
	var entryVersion string
	for _, entry := range entries {
		api.publishTransportToCXO(entry.Entry)
		if api.dhtMirror != nil {
			seq := uint64(time.Now().UnixNano()) //nolint:gosec
			for _, edgePK := range entry.Entry.Edges {
				api.dhtMirror.Mirror(edgePK, entry.Entry, seq)
			}
		}
		if entryVersion == "" && entry.Version != "" {
			entryVersion = entry.Version
		}
		if err := api.store.RecordTransportHeartbeat(r.Context(), entry.Entry.ID, string(entry.Entry.Type)); err != nil {
			api.log(r).WithError(err).Debug("Failed to record transport heartbeat")
		}
	}

	// Record a heartbeat for the registering visor. This piggybacks on
	// the 90s transport re-registration cycle so the TPD's /uptimes
	// endpoint has the same version + daily data as the uptime tracker
	// without requiring a separate heartbeat call from the visor.
	if pk, ok := r.Context().Value(httpauth.ContextAuthKey).(cipher.PubKey); ok {
		if err := api.store.RecordHeartbeat(r.Context(), pk, entryVersion); err != nil {
			api.log(r).WithError(err).Debug("Failed to record heartbeat from transport registration")
		}
	}

	// Check if sync=true query param is set - return all transports for local route calculation
	syncParam := r.URL.Query().Get("sync")
	if syncParam == "true" {
		allEntries, err := api.store.GetAllTransports(r.Context(), false)
		if err != nil {
			api.log(r).WithError(err).Error("Error getting all transports for sync")
			api.writeError(w, r, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusCreated, allEntries)
		return
	}

	httputil.WriteJSON(w, r, http.StatusCreated, entries)
}

func (api *API) getTransportByID(w http.ResponseWriter, r *http.Request) {
	idParam := chi.URLParam(r, "id")

	id, err := uuid.Parse(idParam)
	if err != nil {
		api.writeError(w, r, ErrInvalidTransportID)
		return
	}

	entry, err := api.store.GetTransportByID(r.Context(), id)
	if err != nil {
		api.writeError(w, r, err)
		return
	}

	httputil.WriteJSON(w, r, http.StatusOK, entry)
}

// POST /transports/edges
// Accepts a JSON array of PK hex strings and returns all transports
// involving any of those PKs. More efficient than calling
// GET /transports/edge:{edge} multiple times or fetching /all-transports.
func (api *API) getTransportsByEdges(w http.ResponseWriter, r *http.Request) {
	var pks []string
	if err := json.NewDecoder(r.Body).Decode(&pks); err != nil {
		api.writeError(w, r, ErrInvalidPubKey)
		return
	}

	seen := make(map[uuid.UUID]bool)
	var result []*transport.Entry
	for _, pkStr := range pks {
		pk := cipher.PubKey{}
		if err := pk.UnmarshalText([]byte(strings.TrimSpace(pkStr))); err != nil {
			continue
		}
		entries, err := api.store.GetTransportsByEdge(r.Context(), pk)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !seen[e.ID] {
				seen[e.ID] = true
				result = append(result, e)
			}
		}
	}

	if result == nil {
		result = []*transport.Entry{}
	}
	httputil.WriteJSON(w, r, http.StatusOK, result)
}

func (api *API) getTransportByEdge(w http.ResponseWriter, r *http.Request) {
	edgeParam := chi.URLParam(r, "edge")

	pk := cipher.PubKey{}
	if err := pk.UnmarshalText([]byte(edgeParam)); err != nil {
		api.log(r).WithError(err).Error("Error parsing PK")
		api.writeError(w, r, ErrInvalidPubKey)
		return
	}

	entries, err := api.store.GetTransportsByEdge(r.Context(), pk)
	if err != nil {
		if err != store.ErrTransportNotFound {
			api.log(r).WithError(err).Error("Error getting transport")
		}
		api.writeError(w, r, err)
		return
	}
	httputil.WriteJSON(w, r, http.StatusOK, entries)
}

func (api *API) getTransportStats(w http.ResponseWriter, r *http.Request) {
	edgeParam := chi.URLParam(r, "edge")

	pk := cipher.PubKey{}
	if err := pk.UnmarshalText([]byte(edgeParam)); err != nil {
		api.log(r).WithError(err).Error("Error parsing PK")
		api.writeError(w, r, ErrInvalidPubKey)
		return
	}

	entries, err := api.store.GetTransportsByEdge(r.Context(), pk)
	if err != nil {
		if err != store.ErrTransportNotFound {
			api.log(r).WithError(err).Error("Error getting transport count")
		}
		api.writeError(w, r, err)
		return
	}

	// Break down counts by transport type
	byType := make(map[string]int)
	for _, entry := range entries {
		byType[string(entry.Type)]++
	}

	stats := map[string]interface{}{
		"total":   len(entries),
		"by_type": byType,
	}

	httputil.WriteJSON(w, r, http.StatusOK, stats)
}

func (api *API) getAllTransports(w http.ResponseWriter, r *http.Request) {
	selfTransports := true
	query := r.URL.Query()
	selfTransportsParam := query.Get("selfTransports")
	if selfTransportsParam == "hide" {
		selfTransports = false
	}
	entries := api.getTransportsFromCache(selfTransports)
	if entries == nil {
		var err error
		entries, err = api.store.GetAllTransports(r.Context(), selfTransports)
		if err != nil {
			api.writeError(w, r, err)
			return
		}
	}
	if len(entries) == 0 {
		api.writeError(w, r, store.ErrTransportNotFound)
		return
	}
	httputil.WriteJSON(w, r, http.StatusOK, entries)
}

func (api *API) getAllTransportsStats(w http.ResponseWriter, r *http.Request) {
	selfTransports := true
	query := r.URL.Query()
	selfTransportsParam := query.Get("selfTransports")
	if selfTransportsParam == "hide" {
		selfTransports = false
	}

	entries := api.getTransportsFromCache(selfTransports)
	if entries == nil {
		var err error
		entries, err = api.store.GetAllTransports(r.Context(), selfTransports)
		if err != nil {
			api.writeError(w, r, err)
			return
		}
	}
	if len(entries) == 0 {
		api.writeError(w, r, store.ErrTransportNotFound)
		return
	}

	// Calculate network-wide statistics
	byType := make(map[string]int)
	uniqueVisors := make(map[cipher.PubKey]struct{})

	for _, entry := range entries {
		byType[string(entry.Type)]++
		for _, edge := range entry.Edges {
			uniqueVisors[edge] = struct{}{}
		}
	}

	stats := map[string]interface{}{
		"total_transports": len(entries),
		"by_type":          byType,
		"unique_visors":    len(uniqueVisors),
	}

	httputil.WriteJSON(w, r, http.StatusOK, stats)
}

func (api *API) getAllTransportsPerKeyStats(w http.ResponseWriter, r *http.Request) {
	selfTransports := true
	query := r.URL.Query()
	selfTransportsParam := query.Get("selfTransports")
	if selfTransportsParam == "hide" {
		selfTransports = false
	}

	entries := api.getTransportsFromCache(selfTransports)
	if entries == nil {
		var err error
		entries, err = api.store.GetAllTransports(r.Context(), selfTransports)
		if err != nil {
			api.writeError(w, r, err)
			return
		}
	}
	if len(entries) == 0 {
		api.writeError(w, r, store.ErrTransportNotFound)
		return
	}

	// Build per-key statistics: map[pkHex]map[typeOrTotal]count
	// Format: {"pk1": {"total": 15, "stcpr": 1, "sudph": 14}, ...}
	result := make(map[string]map[string]int)

	for _, entry := range entries {
		for _, edge := range entry.Edges {
			pkHex := edge.Hex()
			if result[pkHex] == nil {
				result[pkHex] = make(map[string]int)
			}
			result[pkHex][string(entry.Type)]++
			result[pkHex]["total"]++
		}
	}

	httputil.WriteJSON(w, r, http.StatusOK, result)
}

func (api *API) deleteTransport(w http.ResponseWriter, r *http.Request) {
	pk, ok := r.Context().Value(httpauth.ContextAuthKey).(cipher.PubKey)
	if !ok {
		api.writeError(w, r, errors.New("invalid auth, no public key provided"))
		return
	}

	idParam := chi.URLParam(r, "id")

	id, err := uuid.Parse(idParam)
	if err != nil {
		api.writeError(w, r, ErrInvalidTransportID)
		return
	}

	entry, err := api.store.GetTransportByID(r.Context(), id)
	if err != nil {
		api.writeError(w, r, err)
		return
	}

	if entry.EdgeIndex(pk) < 0 {
		api.writeError(w, r, ErrInvalidTransportID)
		return
	}

	err = api.store.DeregisterTransport(r.Context(), id)
	if err != nil {
		api.writeError(w, r, err)
		return
	}

	// Remove from CXO feed
	api.unpublishTransportFromCXO(id.String())

	w.WriteHeader(http.StatusOK)
	if _, err = w.Write([]byte("transport deleted")); err != nil {
		api.writeError(w, r, err)
	}
}

// deleteTransportsBatch deletes multiple transports in a single request.
// The caller must be an edge on each transport. Only transports where the caller
// is an edge will be deleted; others are silently skipped.
func (api *API) deleteTransportsBatch(w http.ResponseWriter, r *http.Request) {
	pk, ok := r.Context().Value(httpauth.ContextAuthKey).(cipher.PubKey)
	if !ok {
		api.writeError(w, r, errors.New("invalid auth, no public key provided"))
		return
	}

	var ids []string
	body, err := io.ReadAll(r.Body)
	if err != nil {
		api.writeError(w, r, ErrBadInput)
		return
	}
	if err := json.Unmarshal(body, &ids); err != nil {
		api.writeError(w, r, ErrBadInput)
		return
	}

	deleted := 0
	skipped := 0
	for _, idParam := range ids {
		id, err := uuid.Parse(idParam)
		if err != nil {
			skipped++
			continue
		}

		entry, err := api.store.GetTransportByID(r.Context(), id)
		if err != nil {
			skipped++
			continue
		}

		// Only allow deletion if caller is an edge on this transport
		if entry.EdgeIndex(pk) < 0 {
			skipped++
			continue
		}

		if err := api.store.DeregisterTransport(r.Context(), id); err != nil {
			skipped++
			continue
		}
		api.unpublishTransportFromCXO(id.String())
		deleted++
	}

	httputil.WriteJSON(w, r, http.StatusOK, map[string]int{"deleted": deleted, "skipped": skipped})
}

func (api *API) deregisterTransport(w http.ResponseWriter, r *http.Request) {
	api.log(r).Info("Deregistration process started.")

	nmPkString := r.Header.Get("NM-PK")
	if ok := WhitelistPKs.Get(nmPkString); !ok {
		api.log(r).WithError(ErrUnauthorizedNetworkMonitor).WithField("Step", "Checking NMs PK").Error("Deregistration process interrupt.")
		w.WriteHeader(http.StatusForbidden)
		return
	}

	nmPk := cipher.PubKey{}
	if err := nmPk.UnmarshalText([]byte(nmPkString)); err != nil {
		api.log(r).WithError(ErrBadInput).WithField("Step", "Reading NMs PK").Error("Deregistration process interrupt.")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	nmSign := cipher.Sig{}
	if err := nmSign.UnmarshalText([]byte(r.Header.Get("NM-Sign"))); err != nil {
		api.log(r).WithError(ErrBadInput).WithField("Step", "Checking sign").Error("Deregistration process interrupt.")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if err := cipher.VerifyPubKeySignedPayload(nmPk, nmSign, []byte(nmPk.Hex())); err != nil {
		api.log(r).WithError(ErrUnauthorizedNetworkMonitor).WithField("Step", "Verifying request").Error("Deregistration process interrupt.")
		w.WriteHeader(http.StatusForbidden)
		return
	}

	var tps []string
	body, err := io.ReadAll(r.Body)
	if err != nil {
		api.log(r).WithError(ErrBadInput).WithField("Step", "Reading transports ids").Error("Deregistration process interrupt.")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if err := json.Unmarshal(body, &tps); err != nil {
		api.log(r).WithError(ErrBadInput).WithField("Step", "Unmarshal transports ids").Error("Deregistration process interrupt.")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	for _, idParam := range tps {
		id, err := uuid.Parse(idParam)
		if err != nil {
			api.writeError(w, r, ErrInvalidTransportID)
			continue
		}
		err = api.store.DeregisterTransport(r.Context(), id)
		if err != nil {
			api.writeError(w, r, err)
			continue
		}
		api.unpublishTransportFromCXO(id.String())
	}

	api.log(r).WithFields(logrus.Fields{"Number of Transports": len(tps), "Transports": tps}).Info("Deregistration process completed.")
	httputil.WriteJSON(w, r, http.StatusOK, nil)
}

func (api *API) health(w http.ResponseWriter, r *http.Request) {
	info := buildinfo.Get()
	httputil.WriteJSON(w, r, http.StatusOK, HealthCheckResponse{
		ServiceName: "transport-discovery",
		BuildInfo:   info,
		StartedAt:   api.startedAt,
		DmsgAddr:    api.dmsgAddr,
		DmsgServers: api.DmsgServers,
	})
}

// GET /bandwidth/transport/{id}?period=daily&limit=7
func (api *API) getTransportBandwidth(w http.ResponseWriter, r *http.Request) {
	idParam := chi.URLParam(r, "id")
	id, err := uuid.Parse(idParam)
	if err != nil {
		api.writeError(w, r, ErrInvalidTransportID)
		return
	}

	period := r.URL.Query().Get("period")
	if period == "" {
		period = "daily"
	}

	limit := 7
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	history, err := api.store.GetTransportBandwidth(r.Context(), id, period, limit)
	if err != nil {
		api.writeError(w, r, err)
		return
	}

	httputil.WriteJSON(w, r, http.StatusOK, history)
}

// GET /bandwidth/visor/{pk}?period=daily&limit=7
// Aggregates bandwidth from all transports belonging to a visor
func (api *API) getVisorBandwidth(w http.ResponseWriter, r *http.Request) {
	pkParam := chi.URLParam(r, "pk")
	pk := cipher.PubKey{}
	if err := pk.UnmarshalText([]byte(pkParam)); err != nil {
		api.log(r).WithError(err).Error("Error parsing PK")
		api.writeError(w, r, ErrInvalidPubKey)
		return
	}

	period := r.URL.Query().Get("period")
	if period == "" {
		period = "daily"
	}

	limit := 7
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	history, err := api.store.GetVisorBandwidth(r.Context(), pk, period, limit)
	if err != nil {
		api.writeError(w, r, err)
		return
	}

	httputil.WriteJSON(w, r, http.StatusOK, history)
}

// GET /uptimes
// Query params:
//
//	v=v2   — extended format with version and daily uptime percentages
//	v=v3   — v2 + per-day timeline bitmaps (288 chars per day, '.'=up ' '=down)
//	visors — semicolon-separated PK hex list to filter (required for v3, optional for v1/v2)
func (api *API) getUptimes(w http.ResponseWriter, r *http.Request) {
	version := r.URL.Query().Get("v")
	visorsParam := r.URL.Query().Get("visors")

	// v3 is computed on-demand (timeline bitmaps are expensive for all visors).
	if version == "v3" {
		api.getUptimesV3(w, r, visorsParam)
		return
	}

	if version == "v2" {
		uptimes := api.getUptimesV2FromCache()
		if uptimes == nil {
			uptimes = []store.VisorSummary{}
		}
		if visorsParam != "" {
			uptimes = filterByPKs(uptimes, visorsParam)
		}
		httputil.WriteJSON(w, r, http.StatusOK, uptimes)
		return
	}
	uptimes := api.getUptimesFromCache()
	if uptimes == nil {
		uptimes = []store.VisorSummary{}
	}
	if visorsParam != "" {
		uptimes = filterByPKs(uptimes, visorsParam)
	}
	httputil.WriteJSON(w, r, http.StatusOK, uptimes)
}

// getUptimesV3 computes v3 responses on-demand for specific PKs.
func (api *API) getUptimesV3(w http.ResponseWriter, r *http.Request, visorsParam string) {
	// Get the full v2 cache to start from (has daily + version + online).
	all := api.getUptimesV2FromCache()
	if all == nil {
		all = []store.VisorSummary{}
	}

	// Filter to requested PKs.
	filtered := all
	if visorsParam != "" {
		filtered = filterByPKs(all, visorsParam)
	}

	// Enrich with timeline data.
	now := time.Now().UTC()
	for i := range filtered {
		pkHex := filtered[i].PK.Hex()
		filtered[i].Timeline = api.store.GetDailyTimeline(r.Context(), pkHex, now)
	}

	httputil.WriteJSON(w, r, http.StatusOK, filtered)
}

// bulkUptimesRequest is the JSON body for POST /uptimes. Accepts a
// list of visor PKs and an optional version ("v2"/"v3"). This avoids
// URL-length limits that hit when filtering hundreds of PKs via the
// query-string ?visors= parameter on the GET path.
type bulkUptimesRequest struct {
	PKs     []string `json:"pks"`
	Version string   `json:"v,omitempty"` // "v2" or "v3"; empty = v1
}

// POST /uptimes — bulk visor uptime query.
// Body: {"pks": ["pk1","pk2",...], "v": "v2"}
// Response: same shape as GET /uptimes?v=<v>&visors=<pks>.
func (api *API) postUptimes(w http.ResponseWriter, r *http.Request) {
	var req bulkUptimesRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	visorsParam := strings.Join(req.PKs, ";")
	// Reuse the GET handler logic by injecting the parsed params.
	switch req.Version {
	case "v3":
		api.getUptimesV3(w, r, visorsParam)
	case "v2", "":
		ver := req.Version
		if ver == "" {
			ver = "v1"
		}
		var uptimes []store.VisorSummary
		if ver == "v2" {
			uptimes = api.getUptimesV2FromCache()
		} else {
			uptimes = api.getUptimesFromCache()
		}
		if uptimes == nil {
			uptimes = []store.VisorSummary{}
		}
		if visorsParam != "" {
			uptimes = filterByPKs(uptimes, visorsParam)
		}
		httputil.WriteJSON(w, r, http.StatusOK, uptimes)
	default:
		httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "unknown version: " + req.Version})
	}
}

// bulkTransportUptimesRequest is the JSON body for POST /uptimes/transports.
type bulkTransportUptimesRequest struct {
	PKs   []string `json:"pks,omitempty"`   // visor PKs — returns transports touching these
	IDs   []string `json:"ids,omitempty"`   // transport UUIDs — returns these specific transports
	Type  string   `json:"type,omitempty"`  // filter by transport type
	Edges bool     `json:"edges,omitempty"` // include edge_a / edge_b
	V     string   `json:"v,omitempty"`     // response version
}

// POST /uptimes/transports — bulk transport uptime query.
// Body: {"pks": [...]} or {"ids": [...]} with optional "v", "type", "edges".
// Response: same shape as GET /uptimes/transports with equivalent filters.
func (api *API) postTransportUptimes(w http.ResponseWriter, r *http.Request) {
	var req bulkTransportUptimesRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	v2 := req.V == "v2" || req.V == "v3"
	timeline := req.V == "v3"

	var summaries []store.TransportUptimeSummary
	var err error

	switch {
	case len(req.IDs) > 0:
		ids := make([]uuid.UUID, 0, len(req.IDs))
		for _, s := range req.IDs {
			id, perr := uuid.Parse(strings.TrimSpace(s))
			if perr != nil {
				continue
			}
			ids = append(ids, id)
		}
		summaries, err = api.store.GetTransportUptimeSummaries(r.Context(), ids, v2, timeline)
	case len(req.PKs) > 0:
		for _, pkHex := range req.PKs {
			pkHex = strings.TrimSpace(pkHex)
			if pkHex == "" {
				continue
			}
			var pk cipher.PubKey
			if perr := pk.UnmarshalText([]byte(pkHex)); perr != nil {
				continue
			}
			vs, verr := api.store.GetTransportUptimeByVisor(r.Context(), pk, v2, timeline)
			if verr == nil {
				summaries = append(summaries, vs...)
			}
		}
	default:
		summaries, err = api.store.GetTransportUptimeSummaries(r.Context(), nil, v2, timeline)
	}

	if err != nil {
		api.writeError(w, r, err)
		return
	}

	if req.Type != "" {
		filtered := make([]store.TransportUptimeSummary, 0, len(summaries))
		for _, s := range summaries {
			if s.Type == req.Type {
				filtered = append(filtered, s)
			}
		}
		summaries = filtered
	}
	if !req.Edges {
		for i := range summaries {
			summaries[i].EdgeA = ""
			summaries[i].EdgeB = ""
		}
	}
	if summaries == nil {
		summaries = []store.TransportUptimeSummary{}
	}
	httputil.WriteJSON(w, r, http.StatusOK, summaries)
}

// filterByPKs filters a VisorSummary slice to only include entries whose
// PK hex matches one of the semicolon-separated PKs.
func filterByPKs(summaries []store.VisorSummary, pksParam string) []store.VisorSummary {
	want := make(map[string]struct{})
	for _, pk := range strings.Split(pksParam, ";") {
		pk = strings.TrimSpace(pk)
		if pk != "" {
			want[pk] = struct{}{}
		}
	}
	var result []store.VisorSummary
	for _, s := range summaries {
		if _, ok := want[s.PK.Hex()]; ok {
			result = append(result, s)
		}
	}
	if result == nil {
		result = []store.VisorSummary{}
	}
	return result
}

// GET /v4/update - visor heartbeat for uptime tracking
func (api *API) visorHeartbeat(w http.ResponseWriter, r *http.Request) {
	pk, ok := r.Context().Value(httpauth.ContextAuthKey).(cipher.PubKey)
	if !ok {
		httputil.WriteJSON(w, r, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	version := r.URL.Query().Get("version")
	if err := api.store.RecordHeartbeat(r.Context(), pk, version); err != nil {
		api.log(r).WithError(err).Error("Failed to record heartbeat")
		httputil.WriteJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	httputil.WriteJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// GET /version - returns version statistics (count by version)
// Query params: on=true|false|all|none (filter by online status)
func (api *API) getVersionStats(w http.ResponseWriter, r *http.Request) {
	uptimes := api.getUptimesFromCache()
	if uptimes == nil {
		uptimes = []store.VisorSummary{}
	}

	// Parse 'on' query parameter for online filtering
	onParam := r.URL.Query().Get("on")

	// Count versions
	versionCounts := make(map[string]int)
	for _, vs := range uptimes {
		// Apply online filter
		switch onParam {
		case "true":
			if !vs.Online {
				continue
			}
		case "false":
			if vs.Online {
				continue
			}
		case "all":
			// Include all
		case "none", "":
			// Default: include all (no filtering)
		}

		version := vs.Version
		if version == "" {
			version = "unknown"
		}
		versionCounts[version]++
	}

	httputil.WriteJSON(w, r, http.StatusOK, versionCounts)
}

// VersionEntry is a response entry for version endpoints
type VersionEntry struct {
	PK      string `json:"pk"`
	Version string `json:"version"`
	Online  *bool  `json:"on,omitempty"` // Only included if status=true
}

// GET /versions - returns all PKs with their versions
// Query params:
//   - on=true|false|all|none (filter by online status)
//   - status=true (include online status in response)
func (api *API) getVersions(w http.ResponseWriter, r *http.Request) {
	uptimes := api.getUptimesFromCache()
	if uptimes == nil {
		uptimes = []store.VisorSummary{}
	}

	// Parse query parameters
	onParam := r.URL.Query().Get("on")
	includeStatus := r.URL.Query().Get("status") == "true"

	var result []VersionEntry
	for _, vs := range uptimes {
		// Apply online filter
		switch onParam {
		case "true":
			if !vs.Online {
				continue
			}
		case "false":
			if vs.Online {
				continue
			}
		case "all":
			// Include all
		case "none", "":
			// Default: include all (no filtering)
		}

		entry := VersionEntry{
			PK:      vs.PK.Hex(),
			Version: vs.Version,
		}
		if includeStatus {
			online := vs.Online
			entry.Online = &online
		}
		result = append(result, entry)
	}

	if result == nil {
		result = []VersionEntry{}
	}

	httputil.WriteJSON(w, r, http.StatusOK, result)
}

// GET /versions/{pks} - returns versions for specific PKs (comma-separated)
// Query params:
//   - on=true|false|all|none (filter by online status)
//   - status=true (include online status in response)
func (api *API) getVersionsByPKs(w http.ResponseWriter, r *http.Request) {
	pksParam := chi.URLParam(r, "pks")
	if pksParam == "" {
		api.writeError(w, r, ErrEmptyPubKey)
		return
	}

	uptimes := api.getUptimesFromCache()
	if uptimes == nil {
		uptimes = []store.VisorSummary{}
	}

	// Build a map for quick lookup
	uptimeMap := make(map[string]store.VisorSummary)
	for _, vs := range uptimes {
		uptimeMap[vs.PK.Hex()] = vs
	}

	// Parse query parameters
	onParam := r.URL.Query().Get("on")
	includeStatus := r.URL.Query().Get("status") == "true"

	// Parse the comma-separated PKs
	pkStrings := splitPKs(pksParam)

	var result []VersionEntry
	for _, pkStr := range pkStrings {
		vs, found := uptimeMap[pkStr]
		if !found {
			// Include entry with empty version if not found
			entry := VersionEntry{
				PK:      pkStr,
				Version: "",
			}
			if includeStatus {
				online := false
				entry.Online = &online
			}
			result = append(result, entry)
			continue
		}

		// Apply online filter
		switch onParam {
		case "true":
			if !vs.Online {
				continue
			}
		case "false":
			if vs.Online {
				continue
			}
		case "all":
			// Include all
		case "none", "":
			// Default: include all (no filtering)
		}

		entry := VersionEntry{
			PK:      vs.PK.Hex(),
			Version: vs.Version,
		}
		if includeStatus {
			online := vs.Online
			entry.Online = &online
		}
		result = append(result, entry)
	}

	if result == nil {
		result = []VersionEntry{}
	}

	httputil.WriteJSON(w, r, http.StatusOK, result)
}

// splitPKs splits a comma-separated list of public keys
func splitPKs(pks string) []string {
	var result []string
	for _, pk := range strings.Split(pks, ",") {
		pk = strings.TrimSpace(pk)
		if pk != "" {
			result = append(result, pk)
		}
	}
	return result
}

/*
	<<< TRANSPORT UPTIME ENDPOINTS >>>
*/

// GET /uptimes/transports
// Lightweight transport uptime list.
// Query params:
//
//	v=v2       — include type + daily percentages
//	v=v3       — v2 + timeline bitmaps
//	visors     — semicolon-separated PK hex list (transports involving these visors)
//	tp         — semicolon-separated transport ID list
//	type       — filter by transport type (stcpr, sudph)
//	edges=true — include edge_a and edge_b in response
func (api *API) getTransportUptimes(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	version := query.Get("v")
	visorsParam := query.Get("visors")
	tpParam := query.Get("tp")
	tpType := query.Get("type")
	includeEdges := query.Get("edges") == "true"
	v2 := version == "v2" || version == "v3"
	timeline := version == "v3"

	var summaries []store.TransportUptimeSummary
	var err error

	if tpParam != "" {
		// Filter by specific transport IDs.
		ids := parseTpIDs(tpParam)
		summaries, err = api.store.GetTransportUptimeSummaries(r.Context(), ids, v2, timeline)
	} else if visorsParam != "" {
		// Filter by visor PKs — get all transports for those visors.
		for _, pkHex := range strings.Split(visorsParam, ";") {
			pkHex = strings.TrimSpace(pkHex)
			if pkHex == "" {
				continue
			}
			var pk cipher.PubKey
			if perr := pk.UnmarshalText([]byte(pkHex)); perr != nil {
				continue
			}
			visorSummaries, verr := api.store.GetTransportUptimeByVisor(r.Context(), pk, v2, timeline)
			if verr == nil {
				summaries = append(summaries, visorSummaries...)
			}
		}
	} else {
		// All transports seen today — use the online set.
		summaries, err = api.store.GetTransportUptimeSummaries(r.Context(), nil, v2, timeline)
	}

	if err != nil {
		api.writeError(w, r, err)
		return
	}

	// Filter by type if specified.
	if tpType != "" {
		filtered := make([]store.TransportUptimeSummary, 0, len(summaries))
		for _, s := range summaries {
			if s.Type == tpType {
				filtered = append(filtered, s)
			}
		}
		summaries = filtered
	}

	// Strip edges unless requested.
	if !includeEdges {
		for i := range summaries {
			summaries[i].EdgeA = ""
			summaries[i].EdgeB = ""
		}
	}

	if summaries == nil {
		summaries = []store.TransportUptimeSummary{}
	}
	httputil.WriteJSON(w, r, http.StatusOK, summaries)
}

// GET /metrics/uptime
// Network-wide transport uptime aggregate.
func (api *API) getNetworkTransportUptime(w http.ResponseWriter, r *http.Request) {
	// Get all transport summaries (no filter = all seen today).
	summaries, err := api.store.GetTransportUptimeSummaries(r.Context(), nil, false, false)
	if err != nil {
		api.writeError(w, r, err)
		return
	}

	total := len(summaries)
	online := 0
	byType := make(map[string]struct{ Total, Online int })

	for _, s := range summaries {
		if s.Online {
			online++
		}
		entry := byType[s.Type]
		entry.Total++
		if s.Online {
			entry.Online++
		}
		byType[s.Type] = entry
	}

	type typeStats struct {
		Total  int `json:"total"`
		Online int `json:"online"`
	}
	resp := struct {
		Total  int                  `json:"total_transports"`
		Online int                  `json:"online"`
		ByType map[string]typeStats `json:"by_type"`
	}{
		Total:  total,
		Online: online,
		ByType: make(map[string]typeStats),
	}
	for t, s := range byType {
		resp.ByType[t] = typeStats{Total: s.Total, Online: s.Online}
	}

	httputil.WriteJSON(w, r, http.StatusOK, resp)
}

// GET /metrics/uptime/{ids}
// Transport uptime for specific transport IDs (comma-separated).
func (api *API) getTransportUptimeByIDs(w http.ResponseWriter, r *http.Request) {
	idsParam := chi.URLParam(r, "ids")
	ids, err := parseIDs(idsParam)
	if err != nil || len(ids) == 0 {
		api.writeError(w, r, ErrInvalidTransportID)
		return
	}

	query := r.URL.Query()
	version := query.Get("v")
	v2 := version == "v2" || version == "v3"
	timeline := version == "v3"
	includeEdges := query.Get("edges") == "true"

	summaries, err := api.store.GetTransportUptimeSummaries(r.Context(), ids, v2, timeline)
	if err != nil {
		api.writeError(w, r, err)
		return
	}

	if !includeEdges {
		for i := range summaries {
			summaries[i].EdgeA = ""
			summaries[i].EdgeB = ""
		}
	}

	httputil.WriteJSON(w, r, http.StatusOK, summaries)
}

// GET /metrics/uptime/visor/{pks}
// Transport uptime for transports of specific visors (comma-separated PKs).
func (api *API) getTransportUptimeByVisors(w http.ResponseWriter, r *http.Request) {
	pksParam := chi.URLParam(r, "pks")
	pks, err := parsePKs(pksParam)
	if err != nil || len(pks) == 0 {
		api.writeError(w, r, ErrInvalidPubKey)
		return
	}

	query := r.URL.Query()
	version := query.Get("v")
	v2 := version == "v2" || version == "v3"
	timeline := version == "v3"
	includeEdges := query.Get("edges") == "true"

	var summaries []store.TransportUptimeSummary
	for _, pk := range pks {
		visorSummaries, verr := api.store.GetTransportUptimeByVisor(r.Context(), pk, v2, timeline)
		if verr == nil {
			summaries = append(summaries, visorSummaries...)
		}
	}

	if !includeEdges {
		for i := range summaries {
			summaries[i].EdgeA = ""
			summaries[i].EdgeB = ""
		}
	}

	if summaries == nil {
		summaries = []store.TransportUptimeSummary{}
	}
	httputil.WriteJSON(w, r, http.StatusOK, summaries)
}

// parseTpIDs parses semicolon-separated transport UUIDs.
func parseTpIDs(param string) []uuid.UUID {
	var ids []uuid.UUID
	for _, s := range strings.Split(param, ";") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if id, err := uuid.Parse(s); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}
