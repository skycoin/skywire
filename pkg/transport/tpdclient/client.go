// Package tpdclient implements transport discovery client
package tpdclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/internal/httpauth"
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
