// Package arclient implements address resolver client
package arclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/SkycoinProject/skywire-mainnet/internal/httpauth"
)

var log = logging.MustGetLogger("arclient")

const (
	bindPath    = "/bind"
	resolvePath = "/resolve/"
)

var (
	// ErrNoEntry means that there exists no entry for this PK.
	ErrNoEntry = errors.New("no entry for this PK")
)

// Error is the object returned to the client when there's an error.
type Error struct {
	Error string `json:"error"`
}

// APIClient implements DMSG discovery API client.
type APIClient interface {
	SetTransport(transport http.RoundTripper)
	Bind(ctx context.Context, port string) error
	Resolve(ctx context.Context, pk cipher.PubKey) (string, error)
}

// httpClient implements Client for uptime tracker API.
type httpClient struct {
	client *httpauth.Client
	pk     cipher.PubKey
	sk     cipher.SecKey
}

// NewHTTP creates a new client setting a public key to the client to be used for auth.
// When keys are set, the client will sign request before submitting.
// The signature information is transmitted in the header using:
// * SW-Public: The specified public key
// * SW-Nonce:  The nonce for that public key
// * SW-Sig:    The signature of the payload + the nonce
func NewHTTP(addr string, pk cipher.PubKey, sk cipher.SecKey) (APIClient, error) {
	client, err := httpauth.NewClient(context.Background(), addr, pk, sk)
	if err != nil {
		return nil, fmt.Errorf("httpauth: %w", err)
	}

	return &httpClient{client: client, pk: pk, sk: sk}, nil
}

// Get performs a new GET request.
func (c *httpClient) Get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, c.client.Addr()+path, new(bytes.Buffer))
	if err != nil {
		return nil, err
	}

	req.Close = false
	req.Header.Set("Connection", "keep-alive")

	return c.client.Do(req.WithContext(ctx))
}

// Post performs a POST request.
func (c *httpClient) Post(ctx context.Context, path string, payload interface{}) (*http.Response, error) {
	body := bytes.NewBuffer(nil)
	if err := json.NewEncoder(body).Encode(payload); err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, c.client.Addr()+path, body)
	if err != nil {
		return nil, err
	}

	req.Close = false
	req.Header.Set("Connection", "keep-alive")

	return c.client.Do(req.WithContext(ctx))
}

func (c *httpClient) SetTransport(transport http.RoundTripper) {
	c.client.SetTransport(transport)
}

// BindRequest stores bind request values.
type BindRequest struct {
	Port string `json:"port"`
}

// Bind binds client PK to IP:port on address resolver.
func (c *httpClient) Bind(ctx context.Context, port string) error {
	req := BindRequest{
		Port: port,
	}

	resp, err := c.Post(ctx, bindPath, req)
	if err != nil {
		return err
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.WithError(err).Warn("Failed to close response body")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status: %d, error: %v", resp.StatusCode, extractError(resp.Body))
	}

	return nil
}

// ResolveResponse stores response response values.
type ResolveResponse struct {
	Addr string `json:"addr"`
}

func (c *httpClient) Resolve(ctx context.Context, pk cipher.PubKey) (string, error) {
	resp, err := c.Get(ctx, resolvePath+pk.String())
	if err != nil {
		return "", err
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.WithError(err).Warn("Failed to close response body")
		}
	}()

	if resp.StatusCode == http.StatusNotFound {
		return "", ErrNoEntry
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status: %d, error: %v", resp.StatusCode, extractError(resp.Body))
	}

	rawBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var resolveResp ResolveResponse

	if err := json.Unmarshal(rawBody, &resolveResp); err != nil {
		return "", err
	}

	return resolveResp.Addr, nil
}

// extractError returns the decoded error message from Body.
func extractError(r io.Reader) error {
	var apiError Error

	body, err := ioutil.ReadAll(r)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(body, &apiError); err != nil {
		return errors.New(string(body))
	}

	return errors.New(apiError.Error)
}
