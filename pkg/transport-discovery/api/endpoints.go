// Package api pkg/transport-discovery/endpoints.go
package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

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

	for _, entry := range entries {
		if err := api.store.RegisterTransport(r.Context(), entry); err != nil {
			api.writeError(w, r, err)
			return
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
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(allEntries); err != nil {
			api.writeError(w, r, err)
		}
		return
	}

	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(entries); err != nil {
		api.writeError(w, r, err)
	}
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

	if err := json.NewEncoder(w).Encode(entry); err != nil {
		api.writeError(w, r, err)
	}
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
	if err := json.NewEncoder(w).Encode(entries); err != nil {
		api.log(r).WithError(err).Error("Error encoding entries")
		api.writeError(w, r, err)
	}
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

	if err := json.NewEncoder(w).Encode(stats); err != nil {
		api.log(r).WithError(err).Error("Error encoding stats")
		api.writeError(w, r, err)
	}
}

func (api *API) getAllTransports(w http.ResponseWriter, r *http.Request) {
	selfTransports := true
	query := r.URL.Query()
	selfTransportsParam := query.Get("selfTransports")
	if selfTransportsParam == "hide" {
		selfTransports = false
	}
	entries, err := api.store.GetAllTransports(r.Context(), selfTransports)
	if err != nil {
		if err != store.ErrTransportNotFound {
			api.log(r).WithError(err).Error("Error getting transports")
		}
		api.writeError(w, r, err)
		return
	}
	if err := json.NewEncoder(w).Encode(entries); err != nil {
		api.log(r).WithError(err).Error("Error encoding entries")
		api.writeError(w, r, err)
	}
}

func (api *API) getAllTransportsStats(w http.ResponseWriter, r *http.Request) {
	selfTransports := true
	query := r.URL.Query()
	selfTransportsParam := query.Get("selfTransports")
	if selfTransportsParam == "hide" {
		selfTransports = false
	}

	entries, err := api.store.GetAllTransports(r.Context(), selfTransports)
	if err != nil {
		if err != store.ErrTransportNotFound {
			api.log(r).WithError(err).Error("Error getting transports for stats")
		}
		api.writeError(w, r, err)
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

	if err := json.NewEncoder(w).Encode(stats); err != nil {
		api.log(r).WithError(err).Error("Error encoding stats")
		api.writeError(w, r, err)
	}
}

func (api *API) getAllTransportsPerKeyStats(w http.ResponseWriter, r *http.Request) {
	selfTransports := true
	query := r.URL.Query()
	selfTransportsParam := query.Get("selfTransports")
	if selfTransportsParam == "hide" {
		selfTransports = false
	}

	entries, err := api.store.GetAllTransports(r.Context(), selfTransports)
	if err != nil {
		if err != store.ErrTransportNotFound {
			api.log(r).WithError(err).Error("Error getting transports for per-key stats")
		}
		api.writeError(w, r, err)
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

	if err := json.NewEncoder(w).Encode(result); err != nil {
		api.log(r).WithError(err).Error("Error encoding per-key stats")
		api.writeError(w, r, err)
	}
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
		deleted++
	}

	api.writeJSON(w, r, http.StatusOK, map[string]int{"deleted": deleted, "skipped": skipped})
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
	}

	api.log(r).WithFields(logrus.Fields{"Number of Transports": len(tps), "Transports": tps}).Info("Deregistration process completed.")
	api.writeJSON(w, r, http.StatusOK, nil)
}

func (api *API) health(w http.ResponseWriter, r *http.Request) {
	info := buildinfo.Get()
	api.writeJSON(w, r, http.StatusOK, HealthCheckResponse{
		BuildInfo:   info,
		StartedAt:   api.startedAt,
		DmsgAddr:    api.dmsgAddr,
		DmsgServers: api.DmsgServers,
	})
}

func (api *API) writeJSON(w http.ResponseWriter, r *http.Request, code int, object interface{}) {
	jsonObject, err := json.Marshal(object)
	if err != nil {
		api.logger(r).WithError(err).Errorf("failed to encode json response")
		w.WriteHeader(http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	_, err = w.Write(jsonObject)
	if err != nil {
		api.logger(r).WithError(err).Errorf("failed to write json response")
	}
}

func (api *API) logger(r *http.Request) logrus.FieldLogger {
	return httputil.GetLogger(r)
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

	if err := json.NewEncoder(w).Encode(history); err != nil {
		api.log(r).WithError(err).Error("Error encoding bandwidth history")
		api.writeError(w, r, err)
	}
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

	if err := json.NewEncoder(w).Encode(history); err != nil {
		api.log(r).WithError(err).Error("Error encoding bandwidth history")
		api.writeError(w, r, err)
	}
}

// GET /uptimes
func (api *API) getUptimes(w http.ResponseWriter, r *http.Request) {
	uptimes := api.getUptimesFromCache()
	if uptimes == nil {
		uptimes = []store.VisorSummary{}
	}

	if err := json.NewEncoder(w).Encode(uptimes); err != nil {
		api.log(r).WithError(err).Error("Error encoding uptimes")
		api.writeError(w, r, err)
	}
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

	if err := json.NewEncoder(w).Encode(versionCounts); err != nil {
		api.log(r).WithError(err).Error("Error encoding version stats")
		api.writeError(w, r, err)
	}
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

	if err := json.NewEncoder(w).Encode(result); err != nil {
		api.log(r).WithError(err).Error("Error encoding versions")
		api.writeError(w, r, err)
	}
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

	if err := json.NewEncoder(w).Encode(result); err != nil {
		api.log(r).WithError(err).Error("Error encoding versions")
		api.writeError(w, r, err)
	}
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
