package servicedisc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/cipher"

	"github.com/skycoin/skywire/internal/httpauth"
	nu "github.com/skycoin/skywire/internal/netutil"
	"github.com/skycoin/skywire/pkg/util/buildinfo"
	"github.com/skycoin/skywire/pkg/util/netutil"
)

// ErrVisorUnreachable is returned when visor is not reachable
var ErrVisorUnreachable = errors.New("visor is unreachable")

const (
	updateRetryDelay     = 5 * time.Second
	discServiceTypeParam = "type"
	discServiceQtyParam  = "quantity"
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
			Addr:    NewSWAddr(conf.PK, conf.Port),
			Stats:   stats,
			Type:    conf.Type,
			Version: buildinfo.Version(),
		},
		client: http.Client{},
	}
}

func (c *HTTPClient) addr(path, serviceType string, quantity int) (string, error) {
	addr := c.conf.DiscAddr
	url, err := url.Parse(addr)
	if err != nil {
		return "", errors.New("invalid service discovery address in config: " + addr)
	}
	url.Path = path
	q := url.Query()
	if serviceType != "" {
		q.Set(discServiceTypeParam, serviceType)
	}
	if quantity > 1 {
		q.Set(discServiceQtyParam, strconv.Itoa(quantity))
	}
	url.RawQuery = q.Encode()
	return url.String(), nil
}

var (
	authClientMu sync.Mutex
	authClient   *httpauth.Client // Singleton: there should be only one instance per PK.
)

// Auth returns the internal httpauth.Client
func (c *HTTPClient) Auth(ctx context.Context) (*httpauth.Client, error) {
	authClientMu.Lock()
	defer authClientMu.Unlock()

	auth := authClient
	if auth != nil {
		return auth, nil
	}

	auth, err := httpauth.NewClient(ctx, c.conf.DiscAddr, c.conf.PK, c.conf.SK)
	if err != nil {
		return nil, err
	}

	authClient = auth

	return auth, nil
}

// Services calls 'GET /api/services'.
func (c *HTTPClient) Services(ctx context.Context, quantity int) (out []Service, err error) {
	url, err := c.addr("/api/services", c.entry.Type, quantity)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
	return out, err
}

// UpdateEntry calls 'POST /api/services', retrieves the entry
// and updates local field with the result
// if there are no ip addresses in the entry it also tries to fetch those
// from local config
func (c *HTTPClient) UpdateEntry(ctx context.Context) error {
	c.entryMx.Lock()
	defer c.entryMx.Unlock()
	if c.conf.Type == ServiceTypeVisor && len(c.entry.LocalIPs) == 0 {
		ips, err := netutil.DefaultNetworkInterfaceIPs()
		if err != nil {
			return err
		}
		c.entry.LocalIPs = make([]string, 0, len(ips))
		for _, ip := range ips {
			c.entry.LocalIPs = append(c.entry.LocalIPs, ip.String())
		}
	}
	c.entry.Addr = NewSWAddr(c.conf.PK, c.conf.Port) // Just in case.

	entry, err := c.postEntry(ctx)
	if err != nil {
		return err
	}
	c.entry = entry
	return nil
}

// postEntry calls 'POST /api/services' and sends current service entry
// as the payload
func (c *HTTPClient) postEntry(ctx context.Context) (Service, error) {
	auth, err := c.Auth(ctx)
	if err != nil {
		return Service{}, err
	}

	url, err := c.addr("/api/services", "", 1)
	if err != nil {
		return Service{}, nil
	}

	raw, err := json.Marshal(&c.entry)
	if err != nil {
		return Service{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return Service{}, err
	}

	resp, err := auth.Do(req)
	if err != nil {
		return Service{}, err
	}
	if resp != nil {
		defer func() {
			if cErr := resp.Body.Close(); cErr != nil && err == nil {
				err = cErr
			}
		}()
	}

	if resp.StatusCode != http.StatusOK {
		respBody, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return Service{}, fmt.Errorf("read response body: %w", err)
		}

		var hErr HTTPResponse
		if err = json.Unmarshal(respBody, &hErr); err != nil {
			return Service{}, err
		}

		return Service{}, hErr.Error
	}

	var entry Service
	err = json.NewDecoder(resp.Body).Decode(&entry)
	return entry, err
}

// DeleteEntry calls 'DELETE /api/services/{entry_addr}'.
func (c *HTTPClient) DeleteEntry(ctx context.Context) (err error) {
	auth, err := c.Auth(ctx)
	if err != nil {
		return err
	}

	url, err := c.addr("/api/services/"+c.entry.Addr.String(), c.entry.Type, 1)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
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

	ticker := time.NewTicker(updateInterval)
	defer ticker.Stop()

	for {
		if err := c.Update(ctx); errors.Is(err, ErrVisorUnreachable) {
			return
		}
		c.entryMx.Lock()
		j, err := json.Marshal(c.entry)
		c.entryMx.Unlock()
		logger := c.log.WithField("entry", string(j))
		if err == nil {
			logger.Debug("Entry updated.")
		} else {
			logger.Errorf("Service returned malformed entry, error: %s", err)
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Update calls 'POST /api/services' to update service discovery entry
// it performs exponential backoff in case of errors during update, unless
// the error is unrecoverable from
func (c *HTTPClient) Update(ctx context.Context) error {
	retrier := nu.NewRetrier(updateRetryDelay, 0, 2).WithErrWhitelist(ErrVisorUnreachable)
	run := func() error {
		err := c.UpdateEntry(ctx)

		if errors.Is(err, ErrVisorUnreachable) {
			c.log.Errorf("Unable to register visor as public trusted as it's unreachable from WAN")
			return err
		}

		if err != nil {
			c.log.WithError(err).Warn("Failed to update service entry in discovery. Retrying...")
			return err
		}
		return nil
	}
	return retrier.Do(run)
}

// UpdateStats updates the stats field of the internal service entry state.
func (c *HTTPClient) UpdateStats(stats Stats) {
	c.entryMx.Lock()
	c.entry.Stats = &stats
	c.entryMx.Unlock()
}
