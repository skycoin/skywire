// Package dmsgfirst provides a disc.APIClient implementation that
// tries DMSG first and falls back to plain HTTP on transport-level
// failures.
//
// Motivation: dmsg-discovery already serves its HTTP API over DMSG
// (cmd/dmsg/dmsg-discovery/commands/dmsg-discovery.go calls
// dmsghttp.ListenAndServe at the dmsg-discovery's PK on port 80).
// Until this client landed, every caller used disc.NewHTTP which
// goes over plain TCP — meaning a Caddy outage or any HTTP-path
// disruption between the visor / dmsg-server and the dmsg-discovery
// would break entry registration and lookups even though the DMSG
// path was perfectly reachable.
//
// This client makes DMSG the primary path and HTTP a fallback for
// when the DMSG side fails to dial (e.g. the visor's dmsg.Client
// hasn't connected to enough servers yet for the dmsg-discovery PK
// to resolve). Authoritative HTTP responses — ErrKeyNotFound,
// ErrUnauthorized, validation errors — are NOT retried over HTTP;
// only transport-class failures (dial errors, timeouts) trigger
// fallback.
//
// Lives in a sub-package because pkg/dmsg/disc cannot import
// pkg/dmsg/dmsg (that would close an import cycle: dmsg → disc →
// dmsg). Callers that wire this in must import both pkg/dmsg/disc
// (for the APIClient interface and sentinel errors) and this
// package (for the constructor).
package dmsgfirst

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/logging"
)

// New constructs an APIClient that tries the dmsg-discovery's DMSG
// endpoint first and falls back to plain HTTP on transport-level
// failure.
//
// Parameters:
//   - dmsgC: the caller's dmsg.Client. May be nil — in which case the
//     primary path is disabled and the client behaves like
//     disc.NewHTTP. Pass nil from short-lived tools that only have
//     HTTP available.
//   - dmsgdiscPK: the dmsg-discovery's public key. Used to construct
//     the DMSG URL (http://<pk>:80). The caller's dmsgC must be able
//     to resolve this PK — typically via a direct.Client preloaded
//     with the dmsg-discovery's entry, or via successful registration
//     in the discovery itself.
//   - httpURL: the HTTP fallback URL (e.g. "http://dmsgd.skywire.skycoin.com").
//     Same shape callers passed to disc.NewHTTP.
//   - httpClient: optional override for the fallback HTTP client. nil
//     uses http.DefaultClient.
//   - log: logger for both legs.
func New(
	dmsgC *dmsg.Client,
	dmsgdiscPK cipher.PubKey,
	httpURL string,
	httpClient *http.Client,
	log *logging.Logger,
) disc.APIClient {
	if log == nil {
		log = logging.MustGetLogger("disc")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	httpFallback := disc.NewHTTP(httpURL, httpClient, log)

	if dmsgC == nil || dmsgdiscPK.Null() {
		log.WithField("dmsgdisc_pk", dmsgdiscPK.String()).
			Debug("DMSG-first disc: no dmsg client or null pk; using HTTP only")
		return httpFallback
	}

	dmsgURL := fmt.Sprintf("http://%s:%d", dmsgdiscPK.String(), dmsg.DefaultDmsgHTTPPort)
	dmsgTransport := dmsghttp.MakeHTTPTransport(context.Background(), dmsgC)
	dmsgHTTP := &http.Client{Transport: dmsgTransport}
	primary := disc.NewHTTP(dmsgURL, dmsgHTTP, log)

	return &fallbackClient{
		primary:  primary,
		fallback: httpFallback,
		log:      log,
	}
}

// fallbackClient delegates to primary; on a transport-class error,
// retries on fallback. Authoritative errors (entry-not-found, auth,
// validation) bubble up from primary unchanged — they're real
// answers, not symptoms of an unreachable endpoint.
type fallbackClient struct {
	primary  disc.APIClient
	fallback disc.APIClient
	log      *logging.Logger
}

// shouldFallback returns true when err looks like a transport-class
// failure rather than an authoritative discovery response. The
// httpClient layer wraps non-2xx responses in errFromString which
// produces well-known sentinels (ErrKeyNotFound etc.); those must
// NOT trigger fallback or we'd mask a real "this entry doesn't
// exist" with a stale fallback hit.
func shouldFallback(err error) bool {
	if err == nil {
		return false
	}
	// Authoritative discovery sentinels — primary's response is the
	// truth; do not retry on fallback.
	switch {
	case errors.Is(err, disc.ErrKeyNotFound),
		errors.Is(err, disc.ErrNoAvailableServers),
		errors.Is(err, disc.ErrUnauthorized),
		errors.Is(err, disc.ErrBadInput):
		return false
	}
	// EntryValidationError is also authoritative.
	var ve *disc.EntryValidationError
	if errors.As(err, &ve) {
		return false
	}
	// net.Error timeout / temporary — fall back.
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	// url.Error wraps connection-refused, DNS failures, dial errors —
	// the http.Client returns these for transport-level problems.
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	// Last-resort string match for kernel-side errno wrappings that
	// don't satisfy the typed checks above.
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "no route to host"),
		strings.Contains(msg, "i/o timeout"),
		strings.Contains(msg, "connection reset"),
		strings.Contains(msg, "broken pipe"),
		strings.Contains(msg, "no such host"),
		strings.Contains(msg, "context deadline exceeded"):
		return true
	}
	return false
}

func (c *fallbackClient) Entry(ctx context.Context, pk cipher.PubKey) (*disc.Entry, error) {
	if e, err := c.primary.Entry(ctx, pk); !shouldFallback(err) {
		return e, err
	} else { //nolint:revive
		c.log.WithError(err).Debug("disc Entry: DMSG primary failed; trying HTTP fallback")
	}
	return c.fallback.Entry(ctx, pk)
}

func (c *fallbackClient) PostEntry(ctx context.Context, entry *disc.Entry) error {
	if err := c.primary.PostEntry(ctx, entry); !shouldFallback(err) {
		return err
	} else { //nolint:revive
		c.log.WithError(err).Debug("disc PostEntry: DMSG primary failed; trying HTTP fallback")
	}
	return c.fallback.PostEntry(ctx, entry)
}

func (c *fallbackClient) PutEntry(ctx context.Context, sk cipher.SecKey, entry *disc.Entry) error {
	if err := c.primary.PutEntry(ctx, sk, entry); !shouldFallback(err) {
		return err
	} else { //nolint:revive
		c.log.WithError(err).Debug("disc PutEntry: DMSG primary failed; trying HTTP fallback")
	}
	return c.fallback.PutEntry(ctx, sk, entry)
}

func (c *fallbackClient) DelEntry(ctx context.Context, entry *disc.Entry) error {
	if err := c.primary.DelEntry(ctx, entry); !shouldFallback(err) {
		return err
	} else { //nolint:revive
		c.log.WithError(err).Debug("disc DelEntry: DMSG primary failed; trying HTTP fallback")
	}
	return c.fallback.DelEntry(ctx, entry)
}

func (c *fallbackClient) AvailableServers(ctx context.Context) ([]*disc.Entry, error) {
	if v, err := c.primary.AvailableServers(ctx); !shouldFallback(err) {
		return v, err
	} else { //nolint:revive
		c.log.WithError(err).Debug("disc AvailableServers: DMSG primary failed; trying HTTP fallback")
	}
	return c.fallback.AvailableServers(ctx)
}

func (c *fallbackClient) AllServers(ctx context.Context) ([]*disc.Entry, error) {
	if v, err := c.primary.AllServers(ctx); !shouldFallback(err) {
		return v, err
	} else { //nolint:revive
		c.log.WithError(err).Debug("disc AllServers: DMSG primary failed; trying HTTP fallback")
	}
	return c.fallback.AllServers(ctx)
}

func (c *fallbackClient) AllEntries(ctx context.Context) ([]string, error) {
	if v, err := c.primary.AllEntries(ctx); !shouldFallback(err) {
		return v, err
	} else { //nolint:revive
		c.log.WithError(err).Debug("disc AllEntries: DMSG primary failed; trying HTTP fallback")
	}
	return c.fallback.AllEntries(ctx)
}

func (c *fallbackClient) AllClientsByServer(ctx context.Context) (map[string][]*disc.Entry, error) {
	if v, err := c.primary.AllClientsByServer(ctx); !shouldFallback(err) {
		return v, err
	} else { //nolint:revive
		c.log.WithError(err).Debug("disc AllClientsByServer: DMSG primary failed; trying HTTP fallback")
	}
	return c.fallback.AllClientsByServer(ctx)
}

func (c *fallbackClient) ClientsByServer(ctx context.Context, serverPK cipher.PubKey) ([]*disc.Entry, error) {
	if v, err := c.primary.ClientsByServer(ctx, serverPK); !shouldFallback(err) {
		return v, err
	} else { //nolint:revive
		c.log.WithError(err).Debug("disc ClientsByServer: DMSG primary failed; trying HTTP fallback")
	}
	return c.fallback.ClientsByServer(ctx, serverPK)
}
