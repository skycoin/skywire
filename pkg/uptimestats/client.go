// Package uptimestats is a small HTTP client for the discovery-
// integrated uptime endpoints exposed by the service-discovery,
// transport-discovery, and dmsg-discovery services.
//
// The same endpoint shape (/uptimes with optional ?v=v2&v=v3 and
// ?visors= filter) lives on all three discoveries, plus a handful of
// transport-specific endpoints on the transport-discovery. This
// package consolidates the request/response handling so both the CLI
// (cmd/skywire-cli/commands/{sd,mdisc,ut,tp}) and future hypervisor
// RPC / UI code share the same fetcher and types.
package uptimestats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

// Version selects which response shape the endpoint returns.
//
//	V1 (default): pk + online only
//	V2:           adds version + daily uptime percentages
//	V3:           adds per-minute timeline bitmaps (heavy — use sparingly)
type Version string

// Response-shape versions.
const (
	V1 Version = ""
	V2 Version = "v2"
	V3 Version = "v3"
)

// VisorSummary mirrors the shared JSON shape returned by every
// discovery service's /uptimes endpoint. Fields beyond PK + Online
// are populated only when the corresponding Version is requested.
type VisorSummary struct {
	PK       cipher.PubKey     `json:"pk"`
	Online   bool              `json:"on"`
	Version  string            `json:"version,omitempty"`
	Daily    map[string]string `json:"daily,omitempty"`    // YYYY-MM-DD → "0".."100"
	Timeline map[string]string `json:"timeline,omitempty"` // YYYY-MM-DD → bitmap
}

// TransportSummary is the per-transport uptime shape returned by
// transport-discovery's /uptimes/transports endpoint. Edge A/B are
// hex-encoded visor public keys, Type is the transport type
// (stcp/stcpr/sudph/dmsg).
type TransportSummary struct {
	ID       uuid.UUID         `json:"id"`
	Online   bool              `json:"on"`
	Type     string            `json:"type,omitempty"`
	EdgeA    string            `json:"edge_a,omitempty"`
	EdgeB    string            `json:"edge_b,omitempty"`
	Daily    map[string]string `json:"daily,omitempty"`
	Timeline map[string]string `json:"timeline,omitempty"`
}

// NetworkUptime is the shape returned by
// transport-discovery's /metrics/uptime (network-wide aggregate).
//
// Example (live endpoint):
//
//	{"total_transports":12815,"online":5443,
//	 "by_type":{"stcpr":{"total":5735,"online":1135},
//	            "sudph":{"total":7080,"online":4308}}}
type NetworkUptime struct {
	TotalTransports int                      `json:"total_transports"`
	Online          int                      `json:"online"`
	ByType          map[string]TypeBreakdown `json:"by_type,omitempty"`
	Daily           map[string]string        `json:"daily,omitempty"`
}

// TypeBreakdown is the nested per-transport-type total/online pair
// that /metrics/uptime returns under by_type.
type TypeBreakdown struct {
	Total  int `json:"total"`
	Online int `json:"online"`
}

// Client queries a single discovery-service base URL. All fetch
// helpers accept a context so callers can enforce timeouts; the
// default HTTP client's timeout is also set from NewClient.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient returns a Client rooted at baseURL (e.g.
// "https://sd.skycoin.com"). A zero timeout falls back to 30 s.
func NewClient(baseURL string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: timeout},
	}
}

// VisorUptimesOptions tunes /uptimes fetches. Zero value is valid —
// returns every visor with V1 fields.
type VisorUptimesOptions struct {
	Version Version         // V1 / V2 / V3
	Visors  []cipher.PubKey // filter to these PKs (comma-joined into ?visors=)
}

// VisorUptimes fetches the /uptimes endpoint on the base URL. Works
// against sd, tpd, and dmsg-discovery unchanged.
func (c *Client) VisorUptimes(ctx context.Context, opts VisorUptimesOptions) ([]VisorSummary, error) {
	q := url.Values{}
	if opts.Version != V1 {
		q.Set("v", string(opts.Version))
	}
	if len(opts.Visors) > 0 {
		parts := make([]string, len(opts.Visors))
		for i, pk := range opts.Visors {
			parts[i] = pk.Hex()
		}
		q.Set("visors", strings.Join(parts, ";"))
	}
	var out []VisorSummary
	if err := c.getJSON(ctx, "/uptimes", q, &out); err != nil {
		return nil, fmt.Errorf("fetch /uptimes from %s: %w", c.baseURL, err)
	}
	return out, nil
}

// TransportUptimes fetches transport-discovery's /uptimes/transports.
// Returns an error if called against a non-TPD base URL (the path
// 404s on sd / mdisc). Version selects v1/v2/v3 like VisorUptimes.
func (c *Client) TransportUptimes(ctx context.Context, version Version) ([]TransportSummary, error) {
	q := url.Values{}
	if version != V1 {
		q.Set("v", string(version))
	}
	var out []TransportSummary
	if err := c.getJSON(ctx, "/uptimes/transports", q, &out); err != nil {
		return nil, fmt.Errorf("fetch /uptimes/transports from %s: %w", c.baseURL, err)
	}
	return out, nil
}

// NetworkUptime fetches transport-discovery's /metrics/uptime —
// network-wide transport uptime aggregate.
func (c *Client) NetworkUptime(ctx context.Context) (NetworkUptime, error) {
	var out NetworkUptime
	if err := c.getJSON(ctx, "/metrics/uptime", nil, &out); err != nil {
		return NetworkUptime{}, fmt.Errorf("fetch /metrics/uptime from %s: %w", c.baseURL, err)
	}
	return out, nil
}

// TransportUptimeForIDs fetches transport-discovery's
// /metrics/uptime/{ids} — per-transport uptime for explicit IDs.
func (c *Client) TransportUptimeForIDs(ctx context.Context, ids []uuid.UUID) ([]TransportSummary, error) {
	if len(ids) == 0 {
		return nil, errors.New("at least one transport ID required")
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = id.String()
	}
	var out []TransportSummary
	path := "/metrics/uptime/" + strings.Join(parts, ",")
	if err := c.getJSON(ctx, path, nil, &out); err != nil {
		return nil, fmt.Errorf("fetch %s from %s: %w", path, c.baseURL, err)
	}
	return out, nil
}

// TransportUptimeForVisors fetches transport-discovery's
// /metrics/uptime/visor/{pks} — transport uptime scoped to the given
// visor PKs.
func (c *Client) TransportUptimeForVisors(ctx context.Context, pks []cipher.PubKey) ([]TransportSummary, error) {
	if len(pks) == 0 {
		return nil, errors.New("at least one visor PK required")
	}
	parts := make([]string, len(pks))
	for i, pk := range pks {
		parts[i] = pk.Hex()
	}
	var out []TransportSummary
	path := "/metrics/uptime/visor/" + strings.Join(parts, ",")
	if err := c.getJSON(ctx, path, nil, &out); err != nil {
		return nil, fmt.Errorf("fetch %s from %s: %w", path, c.baseURL, err)
	}
	return out, nil
}

func (c *Client) getJSON(ctx context.Context, path string, q url.Values, out any) error {
	u := c.baseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck,gosec
	if resp.StatusCode != http.StatusOK {
		// Read a bounded chunk so a hostile server can't balloon
		// memory before we surface the status code.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096)) //nolint:errcheck
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
