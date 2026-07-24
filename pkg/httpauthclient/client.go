//go:build !tinygo || (js && wasm)

// Package httpauthclient pkg/httpauthclient/client.go c0-com-http
package httpauthclient

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

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
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

// nonceState is the nonce counter + request serialization SHARED by every
// Client that targets the same (addr, PK). httpauth's server keeps a single
// monotonic nonce per PK (redis INCR), but a process legitimately holds several
// clients to the same server as the same identity — e.g. a visor's utclient
// (uptime heartbeat) and tpdclient (transport registration) both hit the TPD.
// Before this, each Client had its own counter + reqMu, so two of them could
// issue concurrent same-PK requests: both passed verifyAuth at nonce N, the
// server INCR'd twice to N+2 while each client advanced only to N+1, and every
// subsequent request 401'd until a resync that kept losing under sustained
// load. That silently suppressed reward-uptime heartbeats for high-transport
// visors. Sharing one counter + one reqMu per (addr, PK) serializes a process's
// requests to a server so it never races its own nonce.
type nonceState struct {
	nonce       uint64     // shared counter; 64-bit-aligned as the first field
	reqMu       sync.Mutex // serializes all Do() calls sharing this (addr, PK)
	initMu      sync.Mutex // guards one-time initial nonce fetch
	initialized bool
}

var (
	nonceStatesMu sync.Mutex
	// nonceStates is keyed by sanitizedAddr+"\x00"+PK. Bounded by the number of
	// distinct (server, identity) pairs a process uses (a handful), so it is
	// intentionally never pruned.
	nonceStates = map[string]*nonceState{}
)

func sharedNonceState(addr string, key cipher.PubKey) *nonceState {
	k := addr + "\x00" + key.Hex()
	nonceStatesMu.Lock()
	defer nonceStatesMu.Unlock()
	st, ok := nonceStates[k]
	if !ok {
		st = &nonceState{}
		nonceStates[k] = st
	}
	return st
}

// Client implements Client for auth services.
type Client struct {
	state          *nonceState // shared per (addr, PK) — see nonceState
	mu             sync.Mutex  // serializes THIS client's own http.Client use
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
	c.state = sharedNonceState(c.addr, key)

	// Establish the shared nonce once per (addr, PK). Additional clients to the
	// same server+identity reuse the shared counter — kept current by the
	// resync-on-401 path in do() — rather than each fetching and racing it.
	c.state.initMu.Lock()
	defer c.state.initMu.Unlock()
	if !c.state.initialized {
		nonce, err := c.Nonce(ctx, c.key)
		if err != nil {
			return nil, err
		}
		atomic.StoreUint64(&c.state.nonce, uint64(nonce))
		c.state.initialized = true
	}

	return c, nil
}

// Do performs a new authenticated Request and returns the response. Internally, if the request was
// successful nonce is incremented
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	return c.do(c.client, req)
}

func (c *Client) do(client *http.Client, req *http.Request) (*http.Response, error) {
	c.state.reqMu.Lock()
	defer c.state.reqMu.Unlock()

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
	atomic.StoreUint64(&c.state.nonce, uint64(n))
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

	return client.Do(req) //nolint:gosec
}

func (c *Client) getCurrentNonce() Nonce {
	return Nonce(atomic.LoadUint64(&c.state.nonce))
}

// IncrementNonce increments client's current nonce.
func (c *Client) IncrementNonce() {
	atomic.AddUint64(&c.state.nonce, 1)
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
