// Package httpauth http authorizatioon
package httpauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
)

const (
	invalidNonceErrorMessage = "SW-Nonce does not match"
)

// Error is the object returned to the client when there's an error.
type Error struct {
	Error string `json:"error"`
}

// NextNonceResponse represents a ServeHTTP response for json encoding
type NextNonceResponse struct {
	Edge      cipher.PubKey `json:"edge"`
	NextNonce Nonce         `json:"next_nonce"`
}

// HTTPResponse represents the http response struct
type HTTPResponse struct {
	Error *HTTPError  `json:"error,omitempty"`
	Data  interface{} `json:"data,omitempty"`
}

// HTTPError is included in an HTTPResponse
type HTTPError struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// Client implements Client for auth services.
type Client struct {
	// atomic requires 64-bit alignment for struct field access
	nonce          uint64
	mu             sync.Mutex
	reqMu          sync.Mutex
	client         *http.Client
	key            cipher.PubKey
	sec            cipher.SecKey
	addr           string // sanitized address of the client, which may differ from addr used in NewClient
	clientPublicIP string // public ip of the local client needed as a header for dmsghttp
	log            *logging.Logger
}

// NewClient creates a new client setting a public key to the client to be used for Auth.
// When keys are set, the client will sign request before submitting.
// The signature information is transmitted in the header using:
// * SW-Public: The specified public key
// * SW-Nonce:  The nonce for that public key
// * SW-Sig:    The signature of the payload + the nonce
func NewClient(ctx context.Context, addr string, key cipher.PubKey, sec cipher.SecKey, client *http.Client, clientPublicIP string,
	mLog *logging.MasterLogger) (*Client, error) {
	c := &Client{
		client:         client,
		key:            key,
		sec:            sec,
		addr:           sanitizedAddr(addr),
		clientPublicIP: clientPublicIP,
		log:            mLog.PackageLogger("httpauth"),
	}

	// request server for a nonce
	nonce, err := c.Nonce(ctx, c.key)
	if err != nil {
		return nil, err
	}
	c.nonce = uint64(nonce)

	return c, nil
}

// Do performs a new authenticated Request and returns the response. Internally, if the request was
// successful nonce is incremented
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	return c.do(c.client, req)
}

func (c *Client) do(client *http.Client, req *http.Request) (*http.Response, error) {
	c.reqMu.Lock()
	defer c.reqMu.Unlock()

	body := make([]byte, 0)
	if req.ContentLength != 0 {
		auxBody, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		if err := req.Body.Close(); err != nil {
			c.log.WithError(err).Warn("Failed to close HTTP request body")
		}
		req.Body = io.NopCloser(bytes.NewBuffer(auxBody))
		body = auxBody
	}

	resp, err := c.doRequest(client, req, body)
	if err != nil {
		return nil, err
	}

	resp, isNonceValid, err := isNonceValid(resp)
	if err != nil {
		return nil, err
	}

	if !isNonceValid {
		nonce, err := c.Nonce(context.Background(), c.key)
		if err != nil {
			return nil, err
		}
		c.SetNonce(nonce)

		if err := resp.Body.Close(); err != nil {
			c.log.WithError(err).Warn("Failed to close HTTP response body")
		}

		req.Body = io.NopCloser(bytes.NewBuffer(body))

		resp, err = c.doRequest(client, req, body)
		if err != nil {
			return nil, err
		}
	}

	if resp.StatusCode == http.StatusOK {
		c.IncrementNonce()
	}

	return resp, nil
}

// Nonce calls the remote API to retrieve the next expected nonce
func (c *Client) Nonce(ctx context.Context, key cipher.PubKey) (Nonce, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	req, err := http.NewRequest(http.MethodGet, c.addr+"/security/nonces/"+key.Hex(), nil)
	if err != nil {
		return 0, err
	}
	req = req.WithContext(ctx)

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			c.log.WithError(err).Warn("Failed to close HTTP response body")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("error getting current nonce: status: %d <- %v", resp.StatusCode, extractHTTPError(resp.Body))
	}

	var nr NextNonceResponse
	if err := json.NewDecoder(resp.Body).Decode(&nr); err != nil {
		return 0, err
	}

	return nr.NextNonce, nil
}

// SetNonce sets client current nonce to given nonce
func (c *Client) SetNonce(n Nonce) {
	atomic.StoreUint64(&c.nonce, uint64(n))
}

// Addr returns sanitized address of the client
func (c *Client) Addr() string {
	return c.addr
}

func (c *Client) doRequest(client *http.Client, req *http.Request, body []byte) (*http.Response, error) {
	nonce := c.getCurrentNonce()
	sign, err := Sign(body, nonce, c.sec)
	if err != nil {
		return nil, err
	}

	// use nonce, later, if no err from req update such nonce
	req.Header.Set("SW-Nonce", strconv.FormatUint(uint64(nonce), 10))
	req.Header.Set("SW-Sig", sign.Hex())
	req.Header.Set("SW-Public", c.key.Hex())
	if c.clientPublicIP != "" {
		req.Header.Set("SW-PublicIP", c.clientPublicIP)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	return client.Do(req)
}

func (c *Client) getCurrentNonce() Nonce {
	return Nonce(atomic.LoadUint64(&c.nonce))
}

// IncrementNonce increments client's current nonce.
func (c *Client) IncrementNonce() {
	atomic.AddUint64(&c.nonce, 1)
}

// isNonceValid checks if `res` contains an invalid nonce error.
// The error is occurred if status code equals to `http.StatusUnauthorized`
// and body contains `invalidNonceErrorMessage`.
func isNonceValid(res *http.Response) (*http.Response, bool, error) {
	var serverResponse HTTPResponse
	var auxResp http.Response

	auxRespBody, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, false, err
	}
	if err := res.Body.Close(); err != nil {
		return nil, false, err
	}
	auxResp = *res
	auxResp.Body = io.NopCloser(bytes.NewBuffer(auxRespBody))

	if err := json.Unmarshal(auxRespBody, &serverResponse); err != nil || serverResponse.Error == nil {
		return &auxResp, true, nil
	}

	isAuthorized := serverResponse.Error.Code != http.StatusUnauthorized
	hasValidNonce := serverResponse.Error.Message != invalidNonceErrorMessage

	return &auxResp, isAuthorized && hasValidNonce, nil
}

func sanitizedAddr(addr string) string {
	if addr == "" {
		return "http://localhost"
	}

	u, err := url.Parse(addr)
	if err != nil {
		return "http://localhost"
	}

	if u.Scheme == "" {
		u.Scheme = "http"
	}

	u.Path = strings.TrimSuffix(u.Path, "/")
	return u.String()
}

// extractHTTPError returns the decoded error message from Body.
func extractHTTPError(r io.Reader) error {
	var serverError HTTPResponse

	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(body, &serverError); err != nil {
		return errors.New(string(body))
	}

	return errors.New(serverError.Error.Message)
}

// ExtractError returns the decoded error message from Body.
func ExtractError(r io.Reader) error {
	var apiError Error

	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(body, &apiError); err != nil {
		return errors.New(string(body))
	}

	return errors.New(apiError.Error)
}
