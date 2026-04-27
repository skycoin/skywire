// Package logserver — pkg/visor/logserver/stats.go: handlers for the
// /stats/* endpoints that expose the visor's local telemetry store.
//
// These routes are mounted under the existing authRoute group, so the
// survey whitelist gates them. The whitelist here is a DoS bound on
// arbitrary historical-range queries — the same data flows openly on
// the visor's CXO publisher feed (added in a follow-up commit), so
// these endpoints are not the confidentiality boundary.
//
// Bitmaps are rendered to 288-char ASCII at the HTTP boundary
// ('.' = set, ' ' = unset), matching the format the Transport
// Discovery already uses for its v3 timeline endpoint so dashboards
// can consume either source unchanged.
package logserver

import (
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/visor/stats"
)

// StatsReader is the read-only view of the visor's telemetry store
// the log server needs. Implemented by *stats.Store via structural
// typing — no explicit assertion needed.
type StatsReader interface {
	AllTransportRecords() ([]*stats.TransportRecord, error)
	GetTransportRecord(id uuid.UUID) (*stats.TransportRecord, error)
	TierNames() ([]string, error)
	TierBitmap(tier string, date time.Time) ([]byte, error)
	TierDates(tier string) ([]string, error)
	ServiceNames() ([]string, error)
	ServiceBitmap(svc string, date time.Time) ([]byte, error)
	ServiceDates(svc string) ([]string, error)
}

// SetStatsReader wires the telemetry store into the log server. Safe
// to call with nil — handlers degrade to "stats unavailable" 503.
func (api *API) SetStatsReader(r StatsReader) {
	api.statsReader = r
}

// statsTransportSnapshot is the over-the-wire shape returned by
// /stats/transports. Mirrors TransportRecord but omits Daily — that
// lives on /stats/transports/history. Keeping the live and historical
// surfaces separate avoids dragging large daily arrays through the
// "what's connected right now?" path.
type statsTransportSnapshot struct {
	ID        uuid.UUID           `json:"id"`
	Edges     []string            `json:"edges"`
	Type      string              `json:"type"`
	Label     string              `json:"label,omitempty"`
	FirstSeen time.Time           `json:"first_seen"`
	LastSeen  time.Time           `json:"last_seen"`
	Current   *stats.LiveSnapshot `json:"current,omitempty"`
}

// statsTransportHistory is the response shape for
// /stats/transports/history: the same record body as snapshot, plus
// the daily rollups filtered to the requested range.
type statsTransportHistory struct {
	ID        uuid.UUID           `json:"id"`
	Edges     []string            `json:"edges"`
	Type      string              `json:"type"`
	Label     string              `json:"label,omitempty"`
	FirstSeen time.Time           `json:"first_seen"`
	LastSeen  time.Time           `json:"last_seen"`
	Daily     []stats.DailyRollup `json:"daily,omitempty"`
}

// statsBitmapView wraps a tier or service's per-day bitmaps as a
// {date: 288-char-ascii} map. Dates absent from the map have no
// data (not necessarily "all offline" — could mean the visor wasn't
// running on that day at all).
type statsBitmapView map[string]string

// registerStatsRoutes attaches the /stats/* group to the given
// auth-gated router. Called from New() after authRoute is built.
func (api *API) registerStatsRoutes(authRoute *gin.RouterGroup) {
	g := authRoute.Group("/stats")
	g.GET("/transports", api.handleStatsTransports)
	g.GET("/transports/history", api.handleStatsTransportsHistory)
	g.GET("/uptime", api.handleStatsUptime)
	g.GET("/services", api.handleStatsServices)
}

func (api *API) handleStatsTransports(c *gin.Context) {
	if api.statsReader == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "stats store not available"})
		return
	}
	records, err := api.statsReader.AllTransportRecords()
	if err != nil {
		api.logger.WithError(err).Warn("Stats: enumerate transports failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]statsTransportSnapshot, 0, len(records))
	for _, rec := range records {
		out = append(out, statsTransportSnapshot{
			ID:        rec.ID,
			Edges:     edgeStrings(rec),
			Type:      rec.Type,
			Label:     rec.Label,
			FirstSeen: rec.FirstSeen,
			LastSeen:  rec.LastSeen,
			Current:   rec.Current,
		})
	}
	c.JSON(http.StatusOK, out)
}

func (api *API) handleStatsTransportsHistory(c *gin.Context) {
	if api.statsReader == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "stats store not available"})
		return
	}
	since, until, err := parseTimeRange(c, defaultHistoryWindow)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Optional id= filter: if set, only return that transport's row.
	if idParam := c.Query("id"); idParam != "" {
		id, err := uuid.Parse(idParam)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id (must be UUID)"})
			return
		}
		rec, err := api.statsReader.GetTransportRecord(id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if rec == nil {
			c.JSON(http.StatusOK, []statsTransportHistory{})
			return
		}
		c.JSON(http.StatusOK, []statsTransportHistory{historyFromRecord(rec, since, until)})
		return
	}

	records, err := api.statsReader.AllTransportRecords()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]statsTransportHistory, 0, len(records))
	for _, rec := range records {
		h := historyFromRecord(rec, since, until)
		if len(h.Daily) == 0 {
			continue
		}
		out = append(out, h)
	}
	c.JSON(http.StatusOK, out)
}

func (api *API) handleStatsUptime(c *gin.Context) {
	if api.statsReader == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "stats store not available"})
		return
	}
	api.handleBitmapEndpoint(c, api.statsReader.TierNames, api.statsReader.TierDates, api.statsReader.TierBitmap)
}

func (api *API) handleStatsServices(c *gin.Context) {
	if api.statsReader == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "stats store not available"})
		return
	}
	api.handleBitmapEndpoint(c, api.statsReader.ServiceNames, api.statsReader.ServiceDates, api.statsReader.ServiceBitmap)
}

// handleBitmapEndpoint is the shared body of /stats/uptime and
// /stats/services — they differ only in which set of buckets they
// scan. Returns a {name: {date: 288-ascii}} map filtered to the
// requested time window.
func (api *API) handleBitmapEndpoint(
	c *gin.Context,
	listNames func() ([]string, error),
	listDates func(string) ([]string, error),
	getBitmap func(string, time.Time) ([]byte, error),
) {
	if api.statsReader == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "stats store not available"})
		return
	}
	since, until, err := parseTimeRange(c, defaultHistoryWindow)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	names, err := listNames()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	sinceKey := since.UTC().Format("2006-01-02")
	untilKey := until.UTC().Format("2006-01-02")

	out := make(map[string]statsBitmapView, len(names))
	for _, name := range names {
		dates, err := listDates(name)
		if err != nil {
			api.logger.WithError(err).WithField("name", name).Warn("Stats: list dates failed")
			continue
		}
		view := statsBitmapView{}
		for _, date := range dates {
			if date < sinceKey || date > untilKey {
				continue
			}
			d, err := time.Parse("2006-01-02", date)
			if err != nil {
				continue
			}
			bm, err := getBitmap(name, d)
			if err != nil {
				continue
			}
			view[date] = stats.Render(bm)
		}
		if len(view) > 0 {
			out[name] = view
		}
	}
	c.JSON(http.StatusOK, out)
}

// defaultHistoryWindow is the implicit `since` when the caller does
// not specify one — seven days back, matching the CXO publisher's
// default rolling window so HTTP and feed answers line up by default.
const defaultHistoryWindow = 7 * 24 * time.Hour

// parseTimeRange reads `since` and `until` query params (RFC3339).
// Missing `since` defaults to now-window; missing `until` defaults
// to now. Validates ordering.
func parseTimeRange(c *gin.Context, defaultWindow time.Duration) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	since := now.Add(-defaultWindow)
	until := now

	if s := c.Query("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return time.Time{}, time.Time{}, errBadTime("since", err)
		}
		since = t.UTC()
	}
	if u := c.Query("until"); u != "" {
		t, err := time.Parse(time.RFC3339, u)
		if err != nil {
			return time.Time{}, time.Time{}, errBadTime("until", err)
		}
		until = t.UTC()
	}
	if until.Before(since) {
		return time.Time{}, time.Time{}, &timeRangeError{msg: "until must be ≥ since"}
	}
	return since, until, nil
}

type timeRangeError struct{ msg string }

func (e *timeRangeError) Error() string { return e.msg }

func errBadTime(field string, cause error) error {
	return &timeRangeError{msg: field + ": " + cause.Error() + " (expected RFC3339)"}
}

// historyFromRecord builds a statsTransportHistory from a stored
// record, slicing Daily to the [since, until] window. Daily entries
// are stored by UTC date string (YYYY-MM-DD) and are kept sorted in
// the record by the tracker's append-only model.
func historyFromRecord(rec *stats.TransportRecord, since, until time.Time) statsTransportHistory {
	sinceKey := since.UTC().Format("2006-01-02")
	untilKey := until.UTC().Format("2006-01-02")
	out := statsTransportHistory{
		ID:        rec.ID,
		Edges:     edgeStrings(rec),
		Type:      rec.Type,
		Label:     rec.Label,
		FirstSeen: rec.FirstSeen,
		LastSeen:  rec.LastSeen,
	}
	for _, d := range rec.Daily {
		if d.Date < sinceKey || d.Date > untilKey {
			continue
		}
		out.Daily = append(out.Daily, d)
	}
	// Daily is already in append order (oldest → newest) but sort
	// defensively so consumers can rely on the contract.
	sort.Slice(out.Daily, func(i, j int) bool { return out.Daily[i].Date < out.Daily[j].Date })
	return out
}

// edgeStrings hex-encodes the two pubkeys on a transport record for
// JSON output. Returns a freshly allocated slice each call.
func edgeStrings(rec *stats.TransportRecord) []string {
	out := make([]string, 0, len(rec.Edges))
	for _, pk := range rec.Edges {
		out = append(out, pk.String())
	}
	return out
}
