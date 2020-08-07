package servicedisc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/cipher"

	"github.com/skycoin/skywire/internal/httpauth"
)

var (
	// ErrVisorUnreachable is returned when visor is unreachable.
	ErrVisorUnreachable = errors.New("visor is unreachable")
)

// Config configures the HTTPClient.
type Config struct {
	Type     string
	PK       cipher.PubKey
	SK       cipher.SecKey
	Port     uint16
	DiscAddr string
}

// HTTPClient is responsible for interacting with the service-discovery
type HTTPClient struct {
	log     logrus.FieldLogger
	conf    Config
	entry   Service
	entryMx sync.Mutex // only used if UpdateLoop && UpdateStats functions are used.
	auth    *httpauth.Client
	client  http.Client
}

// NewClient creates a new HTTPClient.
func NewClient(log logrus.FieldLogger, conf Config) *HTTPClient {
	var stats *Stats
	if conf.Type != ServiceTypeVisor {
		stats = &Stats{ConnectedClients: 0}
	}

	return &HTTPClient{
		log:  log,
		conf: conf,
		entry: Service{
			Addr:  NewSWAddr(conf.PK, conf.Port),
			Stats: stats,
			Type:  conf.Type,
		},
		client: http.Client{},
	}
}

func (c *HTTPClient) addr(path string, sType string) string {
	addr := c.conf.DiscAddr + path

	if sType != "" {
		addr += "?type=" + sType
	}

	return addr
}

// Auth returns the internal httpauth.Client
func (c *HTTPClient) Auth(ctx context.Context) (*httpauth.Client, error) {
	if c.auth != nil {
		return c.auth, nil
	}

	auth, err := httpauth.NewClient(ctx, c.conf.DiscAddr, c.conf.PK, c.conf.SK)
	if err != nil {
		return nil, err
	}

	c.auth = auth
	return auth, nil
}

// Services calls 'GET /api/services'.
func (c *HTTPClient) Services(ctx context.Context) (out []Service, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.addr("/api/services", c.entry.Type), nil)
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
		var hErr HTTPError
		if err = json.NewDecoder(resp.Body).Decode(&hErr); err != nil {
			return nil, err
		}
		return nil, &hErr
	}
	err = json.NewDecoder(resp.Body).Decode(&out)
	return
}

// UpdateEntry calls 'POST /api/services'.
func (c *HTTPClient) UpdateEntry(ctx context.Context) (*Service, error) {
	auth, err := c.Auth(ctx)
	if err != nil {
		return nil, err
	}

	c.entry.Addr = NewSWAddr(c.conf.PK, c.conf.Port) // Just in case.

	raw, err := json.Marshal(&c.entry)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.addr("/api/services", ""), bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}

	resp, err := auth.Do(req)
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
		var hErr HTTPError
		if err = json.NewDecoder(resp.Body).Decode(&hErr); err != nil {
			return nil, err
		}
		return nil, &hErr
	}

	err = json.NewDecoder(resp.Body).Decode(&c.entry)
	return &c.entry, err
}

// DeleteEntry calls 'DELETE /api/services/{entry_addr}'.
func (c *HTTPClient) DeleteEntry(ctx context.Context) (err error) {
	auth, err := c.Auth(ctx)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.addr("/api/services/"+c.entry.Addr.String(), c.entry.Type), nil)
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
		var hErr HTTPError
		if err = json.NewDecoder(resp.Body).Decode(&hErr); err != nil {
			return err
		}
		return &hErr
	}
	return nil
}

// UpdateLoop repetitively calls 'POST /api/services' to update entry.
func (c *HTTPClient) UpdateLoop(ctx context.Context, updateInterval time.Duration) {
	defer func() { _ = c.DeleteEntry(context.Background()) }() //nolint:errcheck

	update := func() {
		for {
			c.entryMx.Lock()
			entry, err := c.UpdateEntry(ctx)
			c.entryMx.Unlock()

			if err != nil {
				if strings.Contains(err.Error(), ErrVisorUnreachable.Error()) {
					c.log.Errorf("Unable to register visor as public trusted as it's unreachable from WAN")
					return
				}

				c.log.WithError(err).Warn("Failed to update service entry in discovery. Retrying...")
				time.Sleep(time.Second * 10) // TODO(evanlinjin): Exponential backoff.
				continue
			}

			c.entryMx.Lock()
			j, err := json.Marshal(entry)
			c.entryMx.Unlock()

			if err != nil {
				panic(err)
			}

			c.log.WithField("entry", string(j)).Debug("Entry updated.")
			return
		}
	}

	// Run initial update.
	update()

	ticker := time.NewTicker(updateInterval)
	for {
		select {
		case <-ctx.Done():
			ticker.Stop()
			return
		case <-ticker.C:
			update()
		}
	}
}

// UpdateStats updates the stats field of the internal service entry state.
func (c *HTTPClient) UpdateStats(stats Stats) {
	c.entryMx.Lock()
	c.entry.Stats = &stats
	c.entryMx.Unlock()
}
