package proxydisc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/sirupsen/logrus"

	"github.com/SkycoinProject/skywire-mainnet/internal/httpauth"
)

// JSONError is the object returned to the client when there's an error.
type JSONError struct {
	Error string `json:"error"`
}

type Config struct {
	PK   cipher.PubKey
	SK   cipher.SecKey
	Addr string
}

type HTTPClient struct {
	log    logrus.FieldLogger
	pk     cipher.PubKey
	sk     cipher.SecKey
	addr   string
	client http.Client
}

func NewClient(log logrus.FieldLogger, conf Config) *HTTPClient {
	return &HTTPClient{
		log:    log,
		pk:     conf.PK,
		sk:     conf.SK,
		addr:   conf.Addr,
		client: http.Client{},
	}
}

func (c *HTTPClient) Proxies(ctx context.Context) (out []Proxy, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.addr+"/api/proxies", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp != nil {
		defer func() {
			if cErr := resp.Body.Close(); cErr != nil && err == nil {
				err = cErr
			}
		}()
	}

	if resp.StatusCode != http.StatusOK {
		var v JSONError
		if err = json.NewDecoder(resp.Body).Decode(&v); err != nil {
			return nil, err
		}
		return nil, errors.New(v.Error)
	}

	err = json.NewDecoder(resp.Body).Decode(&out)
	return
}

func (c *HTTPClient) UpdateEntry(ctx context.Context, auth *httpauth.Client, entry Proxy) (err error) {
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.addr+"/api/proxies", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	resp, err := auth.Do(req)
	if err != nil {
		return err
	}
	if resp != nil {
		defer func() {
			if cErr := resp.Body.Close(); cErr != nil && err == nil {
				err = cErr
			}
		}()
	}

	if resp.StatusCode != http.StatusOK {
		var v JSONError
		if err = json.NewDecoder(resp.Body).Decode(&v); err != nil {
			return err
		}
		return errors.New(v.Error)
	}

	return nil
}

func (c *HTTPClient) UpdateLoop(ctx context.Context, port uint16, updateInterval time.Duration) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var auth *httpauth.Client
	for {
		var err error
		if auth, err = httpauth.NewClient(ctx, c.addr, c.pk, c.sk); err != nil {
			c.log.WithError(err).Warn("Failed to setup auth client. Retrying...")
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second * 5): // TODO(evanlinjin): Exponential backoff.
				continue
			}
		}
		break
	}

	ticker := time.NewTicker(updateInterval)
	defer ticker.Stop()

	update := func() {
		for {
			err := c.UpdateEntry(ctx, auth, Proxy{Addr: NewSWAddr(c.pk, port)})
			if err != nil {
				c.log.WithError(err).Warn("Failed to update proxy entry in discovery. Retrying...")
				time.Sleep(time.Second * 10) // TODO(evanlinjin): Exponential backoff.
				continue
			}
			break
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			update()
		}
	}
}
