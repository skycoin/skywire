// Package disc implements client for dmsg discovery.
package disc

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"sync"
	"time"

	jsoniter "github.com/json-iterator/go"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/dmsg/cipher"
)

var log = logging.MustGetLogger("disc")

var json = jsoniter.ConfigFastest

// APIClient implements dmsg discovery API client.
type APIClient interface {
	Entry(context.Context, cipher.PubKey) (*Entry, error)
	PostEntry(context.Context, *Entry) error
	PutEntry(context.Context, cipher.SecKey, *Entry) error
	AvailableServers(context.Context) ([]*Entry, error)
}

// HTTPClient represents a client that communicates with a dmsg-discovery service through http, it
// implements APIClient
type httpClient struct {
	client    http.Client
	address   string
	updateMux sync.Mutex // for thread-safe sequence incrementing
}

// NewHTTP constructs a new APIClient that communicates with discovery via http.
func NewHTTP(address string) APIClient {
	log.WithField("func", "disc.NewHTTP").
		WithField("addr", address).
		Debug("Created HTTP client.")
	return &httpClient{
		client:  http.Client{},
		address: address,
	}
}

// Entry retrieves an entry associated with the given public key.
func (c *httpClient) Entry(ctx context.Context, publicKey cipher.PubKey) (*Entry, error) {
	endpoint := fmt.Sprintf("%s/dmsg-discovery/entry/%s", c.address, publicKey)
	log := log.WithField("endpoint", endpoint)

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	req = req.WithContext(ctx)

	resp, err := c.client.Do(req)
	if resp != nil {
		defer func() {
			if err := resp.Body.Close(); err != nil {
				log.WithError(err).Warn("Failed to close response body.")
			}
		}()
	}
	if err != nil {
		return nil, err
	}

	// if the response is an error it will be codified as an HTTPMessage
	if resp.StatusCode != http.StatusOK {
		var message HTTPMessage
		err = json.NewDecoder(resp.Body).Decode(&message)
		if err != nil {
			return nil, err
		}

		return nil, errFromString(message.Message)
	}

	var entry Entry
	if err := json.NewDecoder(resp.Body).Decode(&entry); err != nil {
		return nil, err
	}
	return &entry, nil
}

// PostEntry creates a new Entry.
func (c *httpClient) PostEntry(ctx context.Context, e *Entry) error {
	endpoint := c.address + "/dmsg-discovery/entry/"
	log := log.WithField("endpoint", endpoint)

	marshaledEntry, err := json.Marshal(e)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewBuffer(marshaledEntry))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	// Since v0.3.0 visors send ?timeout=true, before v0.3.0 do not.
	q := req.URL.Query()
	q.Add("timeout", "true")
	req.URL.RawQuery = q.Encode()

	req = req.WithContext(ctx)

	resp, err := c.client.Do(req)
	if resp != nil {
		defer func() {
			if err := resp.Body.Close(); err != nil {
				log.WithError(err).Warn("Failed to close response body.")
			}
		}()
	}
	if err != nil {
		log.WithError(err).Error("Failed to perform request.")
		return err
	}

	if resp.StatusCode != http.StatusOK {
		var httpResponse HTTPMessage

		bodyBytes, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		err = json.Unmarshal(bodyBytes, &httpResponse)
		if err != nil {
			return err
		}
		log.WithField("resp_body", httpResponse.Message).
			WithField("resp_status", resp.StatusCode).
			Error()
		return errFromString(httpResponse.Message)
	}
	return nil
}

// PutEntry updates Entry in dmsg discovery.
func (c *httpClient) PutEntry(ctx context.Context, sk cipher.SecKey, entry *Entry) error {
	c.updateMux.Lock()
	defer c.updateMux.Unlock()

	entry.Sequence++
	entry.Timestamp = time.Now().UnixNano()

	for {
		err := entry.Sign(sk)
		if err != nil {
			return err
		}
		err = c.PostEntry(ctx, entry)
		if err == nil {
			return nil
		}
		if err != ErrValidationWrongSequence {
			entry.Sequence--
			return err
		}
		rE, entryErr := c.Entry(ctx, entry.Static)
		if entryErr != nil {
			return err
		}
		if rE.Timestamp > entry.Timestamp { // If there is a more up to date entry drop update
			entry.Sequence = rE.Sequence
			return nil
		}
		entry.Sequence = rE.Sequence + 1
	}
}

// AvailableServers returns list of available servers.
func (c *httpClient) AvailableServers(ctx context.Context) ([]*Entry, error) {
	var entries []*Entry
	endpoint := c.address + "/dmsg-discovery/available_servers"

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)

	resp, err := c.client.Do(req)
	if resp != nil {
		defer func() {
			if err := resp.Body.Close(); err != nil {
				log.WithError(err).Warn("Failed to close response body")
			}
		}()
	}
	if err != nil {
		return nil, err
	}

	// if the response is an error it will be codified as an HTTPMessage
	if resp.StatusCode != http.StatusOK {
		var message HTTPMessage
		err = json.NewDecoder(resp.Body).Decode(&message)
		if err != nil {
			return nil, err
		}

		return nil, errFromString(message.Message)
	}

	err = json.NewDecoder(resp.Body).Decode(&entries)
	if err != nil {
		return nil, err
	}

	return entries, nil
}
