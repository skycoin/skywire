// Package tpdclient pkg/transport/tpdclient/dmsg_client.go
//
// A transport-discovery client that speaks to the TPD over DMSG instead of
// net/http, so it compiles and runs on the TinyGo js/wasm target (a browser
// visor). It performs the SAME signed-auth flow as the HTTP client (client.go,
// //go:build !tinygo): GET the per-key nonce, sign payload+nonce with the
// visor's secret key, send SW-Public/SW-Nonce/SW-Sig headers, and retry once on
// a nonce mismatch. The signing primitives are reused verbatim from
// httpauthclient (nonce.go is net/http-free), so signatures are byte-identical
// to the HTTP client and the same TPD validates both.
package tpdclient

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/httpauthclient"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
)

// invalidNonceMessage mirrors httpauthclient's server-side message (kept local
// so this file does not pull httpauthclient's net/http-bearing client.go).
const invalidNonceMessage = "SW-Nonce does not match"

// dmsgClient is the DMSG-transport implementation of transport.DiscoveryClient.
type dmsgClient struct {
	dmsgC *dmsg.Client
	host  string // TPD public key (hex) or "pk:port"; FetchOverDmsg defaults port 80
	key   cipher.PubKey
	sec   cipher.SecKey
	log   *logging.Logger

	mu    sync.Mutex
	nonce httpauthclient.Nonce
}

// NewDmsg returns a transport.DiscoveryClient that talks to the TPD at tpdPK
// over the given dmsg client, authenticating as key/sec.
func NewDmsg(dmsgC *dmsg.Client, tpdPK cipher.PubKey, key cipher.PubKey, sec cipher.SecKey, mLogger *logging.MasterLogger) transport.DiscoveryClient {
	log := logging.MustGetLogger("tpd_dmsg")
	if mLogger != nil {
		log = mLogger.PackageLogger("tpd_dmsg")
	}
	return &dmsgClient{dmsgC: dmsgC, host: tpdPK.Hex(), key: key, sec: sec, log: log}
}

// fetch performs a signed request to the TPD over dmsg, fetching/refreshing the
// nonce as needed and retrying once on a nonce mismatch (mirrors
// httpauthclient.Client.do). Returns the HTTP status and response body.
func (c *dmsgClient) fetch(ctx context.Context, method, path string, payload interface{}, extraHeaders map[string]string) (int, []byte, error) {
	var body []byte
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		body = b
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.nonce == 0 {
		n, err := c.fetchNonce(ctx)
		if err != nil {
			return 0, nil, err
		}
		c.nonce = n
	}

	status, resp, err := c.signedSend(ctx, method, path, body, extraHeaders)
	if err != nil {
		return 0, nil, err
	}
	if isNonceMismatch(status, resp) {
		n, err := c.fetchNonce(ctx)
		if err != nil {
			return 0, nil, err
		}
		c.nonce = n
		if status, resp, err = c.signedSend(ctx, method, path, body, extraHeaders); err != nil {
			return 0, nil, err
		}
	}
	if status >= 200 && status < 300 {
		c.nonce++
	}
	return status, resp, nil
}

// signedSend signs (body, current nonce) and sends one request over dmsg.
func (c *dmsgClient) signedSend(ctx context.Context, method, path string, body []byte, extraHeaders map[string]string) (int, []byte, error) {
	sig, err := httpauthclient.Sign(body, c.nonce, c.sec)
	if err != nil {
		return 0, nil, err
	}
	headers := map[string]string{
		"SW-Public": c.key.Hex(),
		"SW-Nonce":  strconv.FormatUint(uint64(c.nonce), 10),
		"SW-Sig":    sig.Hex(),
	}
	if len(body) > 0 {
		headers["Content-Type"] = "application/json"
	}
	for k, v := range extraHeaders {
		headers[k] = v
	}
	status, _, respBody, err := dmsgclient.FetchOverDmsg(ctx, c.dmsgC, method, c.host, path, headers, body)
	return status, respBody, err
}

// fetchNonce GETs the next nonce for this visor's key from the TPD.
func (c *dmsgClient) fetchNonce(ctx context.Context) (httpauthclient.Nonce, error) {
	status, _, body, err := dmsgclient.FetchOverDmsg(ctx, c.dmsgC, "GET", c.host, "/security/nonces/"+c.key.Hex(), nil, nil)
	if err != nil {
		return 0, err
	}
	if status != 200 {
		return 0, fmt.Errorf("tpd_dmsg: nonce fetch status %d: %s", status, string(body))
	}
	var nr struct {
		NextNonce uint64 `json:"next_nonce"`
	}
	if err := json.Unmarshal(body, &nr); err != nil {
		return 0, fmt.Errorf("tpd_dmsg: decode nonce: %w", err)
	}
	return httpauthclient.Nonce(nr.NextNonce), nil
}

// isNonceMismatch reports whether a response is the TPD's "bad nonce" rejection
// (HTTP 401 or the SW-Nonce-mismatch message), matching httpauthclient.isNonceValid.
func isNonceMismatch(status int, body []byte) bool {
	if status == 401 {
		return true
	}
	var r struct {
		Error *struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &r) != nil || r.Error == nil {
		return false
	}
	return r.Error.Code == 401 || r.Error.Message == invalidNonceMessage
}

// statusErr returns a non-nil error for a non-2xx status.
func statusErr(status int, body []byte) error {
	if status >= 200 && status < 300 {
		return nil
	}
	return fmt.Errorf("tpd_dmsg: status %d: %s", status, string(body))
}

// --- transport.DiscoveryClient ---

func (c *dmsgClient) RegisterTransports(ctx context.Context, entries ...*transport.SignedEntry) error {
	if len(entries) == 0 {
		return nil
	}
	status, body, err := c.fetch(ctx, "POST", "/transports/", entries, nil)
	if err != nil {
		return err
	}
	return statusErr(status, body)
}

func (c *dmsgClient) RegisterTransportsV3(ctx context.Context, version string, entries ...*transport.Entry) error {
	if len(entries) == 0 {
		return nil
	}
	var hdrs map[string]string
	if version != "" {
		hdrs = map[string]string{"SW-Visor-Version": version}
	}
	status, body, err := c.fetch(ctx, "POST", "/v3/transports/", entries, hdrs)
	if err != nil {
		return err
	}
	if status == 404 {
		// TPD predates /v3/transports/ — fall back to the legacy signed-entry format.
		signed := make([]*transport.SignedEntry, 0, len(entries))
		for _, e := range entries {
			signed = append(signed, &transport.SignedEntry{Entry: e, Version: version})
		}
		return c.RegisterTransports(ctx, signed...)
	}
	return statusErr(status, body)
}

func (c *dmsgClient) GetTransportByID(ctx context.Context, id uuid.UUID) (*transport.Entry, error) {
	status, body, err := c.fetch(ctx, "GET", fmt.Sprintf("/transports/id:%s", id.String()), nil, nil)
	if err != nil {
		return nil, err
	}
	if err := statusErr(status, body); err != nil {
		return nil, err
	}
	var entry transport.Entry
	if err := json.Unmarshal(body, &entry); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}
	return &entry, nil
}

func (c *dmsgClient) GetTransportsByEdge(ctx context.Context, pk cipher.PubKey) ([]*transport.Entry, error) {
	status, body, err := c.fetch(ctx, "GET", fmt.Sprintf("/transports/edge:%s", pk), nil, nil)
	if err != nil {
		return nil, err
	}
	if err := statusErr(status, body); err != nil {
		return nil, err
	}
	var entries []*transport.Entry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}
	return entries, nil
}

func (c *dmsgClient) GetAllTransports(ctx context.Context) ([]*transport.Entry, error) {
	status, body, err := c.fetch(ctx, "GET", "/all-transports", nil, nil)
	if err != nil {
		return nil, err
	}
	if err := statusErr(status, body); err != nil {
		return nil, err
	}
	var entries []*transport.Entry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}
	return entries, nil
}

func (c *dmsgClient) GetTransportStats(ctx context.Context, pk cipher.PubKey) (*transport.TransportStats, error) {
	status, body, err := c.fetch(ctx, "GET", fmt.Sprintf("/transports/stats/%s", pk), nil, nil)
	if err != nil {
		return nil, err
	}
	if err := statusErr(status, body); err != nil {
		return nil, err
	}
	var stats transport.TransportStats
	if err := json.Unmarshal(body, &stats); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}
	return &stats, nil
}

func (c *dmsgClient) GetAllTransportsStats(ctx context.Context) (*transport.NetworkTransportStats, error) {
	status, body, err := c.fetch(ctx, "GET", "/all-transports/stats", nil, nil)
	if err != nil {
		return nil, err
	}
	if err := statusErr(status, body); err != nil {
		return nil, err
	}
	var stats transport.NetworkTransportStats
	if err := json.Unmarshal(body, &stats); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}
	return &stats, nil
}

func (c *dmsgClient) GetAllTransportsPerKeyStats(ctx context.Context) (transport.PerKeyStats, error) {
	status, body, err := c.fetch(ctx, "GET", "/all-transports/per-key-stats", nil, nil)
	if err != nil {
		return nil, err
	}
	if err := statusErr(status, body); err != nil {
		return nil, err
	}
	var stats transport.PerKeyStats
	if err := json.Unmarshal(body, &stats); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}
	return stats, nil
}

func (c *dmsgClient) DeleteTransport(ctx context.Context, id uuid.UUID) error {
	status, body, err := c.fetch(ctx, "DELETE", fmt.Sprintf("/transports/id:%s", id.String()), nil, nil)
	if err != nil {
		return err
	}
	return statusErr(status, body)
}

func (c *dmsgClient) DeleteTransports(ctx context.Context, ids []uuid.UUID) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	idStrings := make([]string, len(ids))
	for i, id := range ids {
		idStrings[i] = id.String()
	}
	status, body, err := c.fetch(ctx, "POST", "/transports/delete-batch", idStrings, nil)
	if err != nil || status == 404 || statusErr(status, body) != nil {
		// Batch unsupported/failed: delete sequentially, counting successes.
		deleted := 0
		for _, id := range ids {
			if derr := c.DeleteTransport(ctx, id); derr == nil {
				deleted++
			}
		}
		return deleted, nil
	}
	return len(ids), nil
}
