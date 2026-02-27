// Package tpdclient implements transport discovery client
package tpdclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/internal/httpauth"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
)

// JSONError is the object returned to the client when there's an error.
type JSONError struct {
	Error string `json:"error"`
}

// apiClient implements Client for discovery API.
type apiClient struct {
	log    *logging.Logger
	client *httpauth.Client
	key    cipher.PubKey
	sec    cipher.SecKey
}

// NewHTTP creates a new client setting a public key to the client to be used for auth.
// When keys are set, the client will sign request before submitting.
// The signature information is transmitted in the header using:
// * SW-Public: The specified public key
// * SW-Nonce:  The nonce for that public key
// * SW-Sig:    The signature of the payload + the nonce
func NewHTTP(addr string, key cipher.PubKey, sec cipher.SecKey, httpC *http.Client, clientPublicIP string, mLogger *logging.MasterLogger) (transport.DiscoveryClient, error) {
	client, err := httpauth.NewClient(context.Background(), addr, key, sec, httpC, clientPublicIP, mLogger)
	if err != nil {
		return nil, fmt.Errorf("transport discovery httpauth: %w", err)
	}
	apiClient := &apiClient{
		log:    mLogger.PackageLogger("transport-discovery"),
		client: client,
		key:    key,
		sec:    sec,
	}
	return apiClient, nil
}

// Post performs a POST request.
func (c *apiClient) Post(ctx context.Context, path string, payload interface{}) (*http.Response, error) {
	body := bytes.NewBuffer(nil)
	if err := json.NewEncoder(body).Encode(payload); err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, c.client.Addr()+path, body)
	if err != nil {
		return nil, err
	}

	return c.client.Do(req.WithContext(ctx))
}

// Get performs a new GET request.
func (c *apiClient) Get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, c.client.Addr()+path, new(bytes.Buffer))
	if err != nil {
		return nil, err
	}

	return c.client.Do(req.WithContext(ctx))
}

// Delete performs a new DELETE request.
func (c *apiClient) Delete(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodDelete, c.client.Addr()+path, new(bytes.Buffer))
	if err != nil {
		return nil, err
	}

	return c.client.Do(req.WithContext(ctx))
}

// RegisterTransports registers new Transports.
func (c *apiClient) RegisterTransports(ctx context.Context, entries ...*transport.SignedEntry) error {
	if len(entries) == 0 {
		return nil
	}

	resp, err := c.Post(ctx, "/transports/", entries)
	if err != nil {
		return err
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			c.log.WithError(err).Warn("Failed to close HTTP response body")
		}
	}()

	return httputil.ErrorFromResp(resp)
}

// GetTransportByID returns Transport for corresponding ID.
func (c *apiClient) GetTransportByID(ctx context.Context, id uuid.UUID) (*transport.Entry, error) {
	resp, err := c.Get(ctx, fmt.Sprintf("/transports/id:%s", id.String()))
	if err != nil {
		return nil, err
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			c.log.WithError(err).Warn("Failed to close HTTP response body")
		}
	}()

	if err := httputil.ErrorFromResp(resp); err != nil {
		return nil, err
	}

	entry := &transport.Entry{}
	if err := json.NewDecoder(resp.Body).Decode(entry); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}

	return entry, nil
}

// GetTransportsByEdge returns all Transports registered for the edge.
func (c *apiClient) GetTransportsByEdge(ctx context.Context, pk cipher.PubKey) ([]*transport.Entry, error) {
	resp, err := c.Get(ctx, fmt.Sprintf("/transports/edge:%s", pk))
	if err != nil {
		return nil, err
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			c.log.WithError(err).Warn("Failed to close HTTP response body")
		}
	}()

	if err := httputil.ErrorFromResp(resp); err != nil {
		return nil, err
	}

	var entries []*transport.Entry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}

	return entries, nil
}

// GetAllTransports returns all registered transports from the discovery service.
// This is useful for caching transport data to reduce API calls.
func (c *apiClient) GetAllTransports(ctx context.Context) ([]*transport.Entry, error) {
	resp, err := c.Get(ctx, "/all-transports")
	if err != nil {
		return nil, err
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			c.log.WithError(err).Warn("Failed to close HTTP response body")
		}
	}()

	if err := httputil.ErrorFromResp(resp); err != nil {
		return nil, err
	}

	var entries []*transport.Entry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}

	return entries, nil
}

// GetTransportStats returns transport statistics for the given public key.
// Returns both total count and breakdown by transport type.
func (c *apiClient) GetTransportStats(ctx context.Context, pk cipher.PubKey) (*transport.TransportStats, error) {
	resp, err := c.Get(ctx, fmt.Sprintf("/transports/stats/%s", pk))
	if err != nil {
		return nil, err
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			c.log.WithError(err).Warn("Failed to close HTTP response body")
		}
	}()

	if err := httputil.ErrorFromResp(resp); err != nil {
		return nil, err
	}

	var stats transport.TransportStats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}

	return &stats, nil
}

// GetAllTransportsStats returns network-wide transport statistics.
// This provides aggregate statistics without returning all individual transport entries.
func (c *apiClient) GetAllTransportsStats(ctx context.Context) (*transport.NetworkTransportStats, error) {
	resp, err := c.Get(ctx, "/all-transports/stats")
	if err != nil {
		return nil, err
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			c.log.WithError(err).Warn("Failed to close HTTP response body")
		}
	}()

	if err := httputil.ErrorFromResp(resp); err != nil {
		return nil, err
	}

	var stats transport.NetworkTransportStats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}

	return &stats, nil
}

// GetAllTransportsPerKeyStats returns transport counts per public key.
// Format: {"pk1": {"total": 15, "stcpr": 1, "sudph": 14}, ...}
// This is useful for finding well-connected visors as fallback when service discovery is empty.
// Supports fallback to old array format: [{"pk":"...", "total":N, "by_type":{...}}, ...]
func (c *apiClient) GetAllTransportsPerKeyStats(ctx context.Context) (transport.PerKeyStats, error) {
	resp, err := c.Get(ctx, "/all-transports/per-key-stats")
	if err != nil {
		return nil, err
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			c.log.WithError(err).Warn("Failed to close HTTP response body")
		}
	}()

	if err := httputil.ErrorFromResp(resp); err != nil {
		return nil, err
	}

	// Read the body once so we can try multiple parse formats
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	// Try new format first: {"pk1": {"total": N, "type": N}, ...}
	var stats transport.PerKeyStats
	if err := json.Unmarshal(body, &stats); err == nil {
		return stats, nil
	}

	// Fallback to old format: [{"pk":"...", "total":N, "by_type":{...}}, ...]
	type oldFormatEntry struct {
		PK     string         `json:"pk"`
		Total  int            `json:"total"`
		ByType map[string]int `json:"by_type"`
	}
	var oldStats []oldFormatEntry
	if err := json.Unmarshal(body, &oldStats); err != nil {
		return nil, fmt.Errorf("json: failed to parse as new or old format: %w", err)
	}

	// Convert old format to new format
	stats = make(transport.PerKeyStats, len(oldStats))
	for _, entry := range oldStats {
		counts := make(map[string]int, len(entry.ByType)+1)
		counts["total"] = entry.Total
		for tpType, count := range entry.ByType {
			counts[tpType] = count
		}
		stats[entry.PK] = counts
	}

	return stats, nil
}

// DeleteTransport deletes given transport by it's ID. A visor can only delete transports if he is one of it's edges.
func (c *apiClient) DeleteTransport(ctx context.Context, id uuid.UUID) error {
	resp, err := c.Delete(ctx, fmt.Sprintf("/transports/id:%s", id.String()))
	if err != nil {
		return err
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			c.log.WithError(err).Warn("Failed to close HTTP response body")
		}
	}()

	return httputil.ErrorFromResp(resp)
}

// DeleteTransports deletes multiple transports in a single request.
// Falls back to sequential deletion if the batch endpoint is unavailable (404).
func (c *apiClient) DeleteTransports(ctx context.Context, ids []uuid.UUID) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	// Convert UUIDs to strings
	idStrings := make([]string, len(ids))
	for i, id := range ids {
		idStrings[i] = id.String()
	}

	// Try batch endpoint first
	resp, err := c.Post(ctx, "/transports/delete-batch", idStrings)
	if err != nil {
		// Network error - fall back to sequential
		c.log.WithError(err).Debug("Batch delete failed, falling back to sequential")
		return c.deleteTransportsSequential(ctx, ids)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			c.log.WithError(err).Warn("Failed to close HTTP response body")
		}
	}()

	// If 404, TPD doesn't support batch delete - fall back to sequential
	if resp.StatusCode == 404 {
		c.log.Debug("TPD does not support batch delete, falling back to sequential")
		return c.deleteTransportsSequential(ctx, ids)
	}

	if err := httputil.ErrorFromResp(resp); err != nil {
		c.log.WithError(err).Debug("Batch delete error, falling back to sequential")
		return c.deleteTransportsSequential(ctx, ids)
	}

	// Parse response
	var result struct {
		Deleted int `json:"deleted"`
		Skipped int `json:"skipped"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		c.log.WithError(err).Warn("Failed to decode batch delete response")
		return result.Deleted, nil
	}

	return result.Deleted, nil
}

// deleteTransportsSequential deletes transports one by one as fallback
func (c *apiClient) deleteTransportsSequential(ctx context.Context, ids []uuid.UUID) (int, error) {
	deleted := 0
	var lastErr error
	for _, id := range ids {
		if err := c.DeleteTransport(ctx, id); err != nil {
			lastErr = err
			c.log.WithError(err).WithField("id", id).Debug("Failed to delete transport")
		} else {
			deleted++
		}
	}
	return deleted, lastErr
}
