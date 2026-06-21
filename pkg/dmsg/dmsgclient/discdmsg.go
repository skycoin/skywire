// Package dmsgclient pkg/dmsg/dmsgclient/discdmsg.go
package dmsgclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// dmsgDiscClient implements disc.APIClient by speaking the dmsg-discovery REST
// API directly over dmsg streams (HTTP/1.1 on dmsg port 80) with NO net/http —
// so it compiles under TinyGo, where net/http is broken on the js target. It is
// the net/http-free equivalent of
//
//	disc.NewHTTP(addr, &http.Client{Transport: dmsghttp.MakeHTTPTransport(...)}, log)
//
// Each call dials a fresh dmsg stream to the discovery PK, runs one HTTP/1.1
// round-trip, and closes it (Connection: close — no keep-alive pool, matching
// the simplicity the wasm hypervisor needs).
type dmsgDiscClient struct {
	dmsgC  *dmsg.Client
	discPK cipher.PubKey
	log    *logging.Logger
	mu     sync.Mutex // serializes the PutEntry sequence bump (mirrors httpClient.updateMux)
}

// NewDmsgDiscClient constructs a net/http-free disc.APIClient that reaches the
// discovery over the given dmsg client's own sessions. discDmsgAddr is a
// "dmsg://<disc-pk>:<port>" address (the same value disc.NewHTTP would receive);
// only the PK is used — the round-trip always targets dmsg port 80.
func NewDmsgDiscClient(dmsgC *dmsg.Client, discDmsgAddr string, log *logging.Logger) (disc.APIClient, error) {
	pkHex := dmsg.ExtractPKFromDmsgAddr(discDmsgAddr)
	if pkHex == "" {
		return nil, fmt.Errorf("dmsgclient: cannot extract discovery PK from %q", discDmsgAddr)
	}
	var discPK cipher.PubKey
	if err := discPK.UnmarshalText([]byte(pkHex)); err != nil {
		return nil, fmt.Errorf("dmsgclient: bad discovery PK %q: %w", pkHex, err)
	}
	return &dmsgDiscClient{dmsgC: dmsgC, discPK: discPK, log: log}, nil
}

// do dials a fresh stream to the discovery and runs one HTTP/1.1 round-trip,
// bounded by ctx (the stream is closed on cancellation so a blocked read/write
// unblocks).
func (c *dmsgDiscClient) do(ctx context.Context, method, path string, reqBody []byte) (httpResult, error) {
	stream, err := c.dmsgC.DialStream(ctx, dmsg.Addr{PK: c.discPK, Port: dmsg.DefaultDmsgHTTPPort})
	if err != nil {
		return httpResult{}, err
	}
	defer stream.Close() //nolint:errcheck

	done := make(chan struct{})
	defer close(done)
	if ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				stream.Close() //nolint:errcheck,gosec
			case <-done:
			}
		}()
	}

	return httpRoundTrip(stream, method, c.discPK.Hex(), path, nil, reqBody)
}

// discMessage mirrors disc.HTTPMessage's `message` field (disc.HTTPMessage
// itself is net/http-tagged, so it isn't visible under TinyGo).
type discMessage struct {
	Message string `json:"message"`
}

// errFromBody maps an error response body back to a sentinel error. Only
// ErrValidationWrongSequence needs to round-trip as a typed value (PutEntry
// branches on it); everything else becomes a plain error carrying the message.
func errFromBody(body []byte) error {
	var m discMessage
	if err := json.Unmarshal(body, &m); err != nil || m.Message == "" {
		return fmt.Errorf("dmsg-discovery error: %s", strings.TrimSpace(string(body)))
	}
	if m.Message == disc.ErrValidationWrongSequence.Error() {
		return disc.ErrValidationWrongSequence
	}
	return errors.New(m.Message)
}

// getJSON runs a GET and decodes a 200 body into out.
func (c *dmsgDiscClient) getJSON(ctx context.Context, path string, out interface{}) error {
	res, err := c.do(ctx, "GET", path, nil)
	if err != nil {
		return err
	}
	if res.status != 200 {
		return errFromBody(res.body)
	}
	return json.Unmarshal(res.body, out)
}

// Entry retrieves an entry associated with the given public key.
func (c *dmsgDiscClient) Entry(ctx context.Context, publicKey cipher.PubKey) (*disc.Entry, error) {
	var entry disc.Entry
	if err := c.getJSON(ctx, "/dmsg-discovery/entry/"+publicKey.String(), &entry); err != nil {
		return nil, err
	}
	return &entry, nil
}

// PostEntry creates or updates an Entry.
func (c *dmsgDiscClient) PostEntry(ctx context.Context, entry *disc.Entry) error {
	body, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	res, err := c.do(ctx, "POST", "/dmsg-discovery/entry/", body)
	if err != nil {
		return err
	}
	if res.status != 200 {
		return errFromBody(res.body)
	}
	return nil
}

// DelEntry deletes an Entry.
func (c *dmsgDiscClient) DelEntry(ctx context.Context, entry *disc.Entry) error {
	body, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	res, err := c.do(ctx, "DELETE", "/dmsg-discovery/entry", body)
	if err != nil {
		return err
	}
	if res.status != 200 {
		return errFromBody(res.body)
	}
	return nil
}

// PutEntry updates Entry in dmsg discovery, signing it and retrying once on a
// wrong-sequence rejection (mirrors httpClient.PutEntry exactly).
func (c *dmsgDiscClient) PutEntry(ctx context.Context, sk cipher.SecKey, entry *disc.Entry) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	sequence := entry.Sequence + 1
	entry.Timestamp = time.Now().UnixNano()

	for {
		entry.Sequence = sequence
		if err := entry.Sign(sk); err != nil {
			return err
		}
		err := c.PostEntry(ctx, entry)
		if err == nil {
			return nil
		}
		if err != disc.ErrValidationWrongSequence {
			return err
		}
		rE, entryErr := c.Entry(ctx, entry.Static)
		if entryErr != nil {
			return entryErr
		}
		if rE.Timestamp > entry.Timestamp { // a more up-to-date entry exists; drop our update
			entry.Sequence = rE.Sequence
			return nil
		}
		sequence = rE.Sequence + 1
	}
}

// AvailableServers returns the list of available servers.
func (c *dmsgDiscClient) AvailableServers(ctx context.Context) ([]*disc.Entry, error) {
	var entries []*disc.Entry
	return entries, c.getJSON(ctx, "/dmsg-discovery/available_servers", &entries)
}

// AllServers returns the list of all servers.
func (c *dmsgDiscClient) AllServers(ctx context.Context) ([]*disc.Entry, error) {
	var entries []*disc.Entry
	return entries, c.getJSON(ctx, "/dmsg-discovery/all_servers", &entries)
}

// AllEntries returns the list of all entry public keys.
func (c *dmsgDiscClient) AllEntries(ctx context.Context) ([]string, error) {
	var entries []string
	return entries, c.getJSON(ctx, "/dmsg-discovery/entries", &entries)
}

// AllClientsByServer returns all client entries grouped by server public key.
func (c *dmsgDiscClient) AllClientsByServer(ctx context.Context) (map[string][]*disc.Entry, error) {
	var result map[string][]*disc.Entry
	return result, c.getJSON(ctx, "/dmsg-discovery/servers/clients", &result)
}

// ClientsByServer returns all client entries delegated to a specific server.
func (c *dmsgDiscClient) ClientsByServer(ctx context.Context, serverPK cipher.PubKey) ([]*disc.Entry, error) {
	var entries []*disc.Entry
	return entries, c.getJSON(ctx, "/dmsg-discovery/server/"+serverPK.Hex()+"/clients", &entries)
}
