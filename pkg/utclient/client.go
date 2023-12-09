// Package utclient implements uptime tracker client
package utclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/internal/httpauth"
)

//go:generate mockery -name APIClient -case underscore -inpkg

// APIClient implements uptime tracker API client.
type APIClient interface {
	UpdateVisorUptime(context.Context, string) error
	FetchUptimes(context.Context, string) ([]byte, error)
}

// httpClient implements Client for uptime tracker API.
type httpClient struct {
	client *httpauth.Client
	pk     cipher.PubKey
	sk     cipher.SecKey
	log    *logging.Logger
}

const (
	createRetryDelay = 5 * time.Second
)

// NewHTTP creates a new client setting a public key to the client to be used for auth.
// When keys are set, the client will sign request before submitting.
// The signature information is transmitted in the header using:
// * SW-Public: The specified public key
// * SW-Nonce:  The nonce for that public key
// * SW-Sig:    The signature of the payload + the nonce
func NewHTTP(addr string, pk cipher.PubKey, sk cipher.SecKey, httpC *http.Client, clientPublicIP string, mLogger *logging.MasterLogger) (APIClient, error) {
	var client *httpauth.Client
	var err error

	log := mLogger.PackageLogger("utclient")

	retrier := netutil.NewRetrier(log, createRetryDelay, 0, 10, 2)
	retrierFunc := func() error {
		client, err = httpauth.NewClient(context.Background(), addr, pk, sk, httpC, clientPublicIP, mLogger)
		if err != nil {
			return fmt.Errorf("uptime tracker httpauth: %w", err)
		}
		return nil
	}

	if err := retrier.Do(context.Background(), retrierFunc); err != nil {
		return nil, err
	}

	httpClient := &httpClient{
		client: client,
		pk:     pk,
		sk:     sk,
		log:    log,
	}

	return httpClient, nil
}

// Get performs a new GET request.
func (c *httpClient) Get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, c.client.Addr()+path, new(bytes.Buffer))
	if err != nil {
		return nil, err
	}

	return c.client.Do(req.WithContext(ctx))
}

// UpdateVisorUptime updates visor uptime.
func (c *httpClient) UpdateVisorUptime(ctx context.Context, version string) error {
	resp, err := c.Get(ctx, fmt.Sprintf("/v4/update?version=%s", version))
	if err != nil {
		return err
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			c.log.WithError(err).Warn("Failed to close response body")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status: %d, error: %w", resp.StatusCode, httpauth.ExtractError(resp.Body))
	}

	return nil
}

// FetchUptimes fetch uptimes data for all visors or specific one
func (c *httpClient) FetchUptimes(ctx context.Context, pk string) ([]byte, error) {
	url := "/uptimes?v=v2"
	if pk != "" {
		url += "&visors=" + pk
	}

	resp, err := c.Get(ctx, url)
	if err != nil {
		return []byte{}, err
	}

	if resp.Body != nil {
		defer func() {
			err = resp.Body.Close()
			if err != nil {
				return
			}
		}()
	}

	return io.ReadAll(resp.Body)
}
