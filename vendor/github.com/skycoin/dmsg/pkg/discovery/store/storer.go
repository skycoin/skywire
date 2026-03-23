// Package store pkg/discovery/store/storer.go
package store

import (
	"context"
	"errors"
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"

	"github.com/skycoin/dmsg/pkg/disc"
)

var log = logging.MustGetLogger("store")

var (
	// ErrTooFewArgs is returned on attempt to create a Redis store without passing its URL.
	ErrTooFewArgs = errors.New("too few args")
)

// EntryStore provides basic CRUD for discovery entries.
type EntryStore interface {
	// Entry obtains a single dmsg instance entry.
	Entry(ctx context.Context, staticPubKey cipher.PubKey) (*disc.Entry, error)

	// SetEntry set's an entry.
	// This is unsafe and does not check signature.
	SetEntry(ctx context.Context, entry *disc.Entry, timeout time.Duration) error

	// DelEntry delete's an entry.
	DelEntry(ctx context.Context, staticPubKey cipher.PubKey) error
}

// ServerLister provides server enumeration.
type ServerLister interface {
	// AvailableServers discovers available dmsg servers.
	AvailableServers(ctx context.Context, maxCount int) ([]*disc.Entry, error)

	// AllServers discovers available dmsg servers.
	AllServers(ctx context.Context) ([]*disc.Entry, error)
}

// EntryEnumerator provides bulk entry access.
type EntryEnumerator interface {
	// CountEntries returns numbers of servers and clients.
	CountEntries(ctx context.Context) (int64, int64, error)

	// AllEntries returns all clients PKs.
	AllEntries(ctx context.Context) ([]string, error)

	// AllVisorEntries returns all clients PKs.
	AllVisorEntries(ctx context.Context) ([]string, error)

	// AllClientEntries returns all full client entries.
	AllClientEntries(ctx context.Context) ([]*disc.Entry, error)
}

// Storer is an interface which allows to implement different kinds of stores
// and choose which one to use in the server
type Storer interface {
	EntryStore
	ServerLister
	EntryEnumerator

	// RemoveOldServerEntries check and remove old server entries that left on redis because of unexpected server shutdown
	RemoveOldServerEntries(ctx context.Context) error
}

// Config configures the Store object.
type Config struct {
	URL      string        // database URI
	Password string        // database password
	Timeout  time.Duration // database entry timeout (0 == none)
}

// Config defaults.
const (
	DefaultURL     = "redis://localhost:6379"
	DefaultTimeout = time.Minute * 3
)

// DefaultConfig returns a config with default values.
func DefaultConfig() *Config {
	return &Config{
		URL:     DefaultURL,
		Timeout: DefaultTimeout,
	}
}

// NewStore returns an initialized store, name represents which
// store to initialize
func NewStore(ctx context.Context, name string, conf *Config, log *logging.Logger) (Storer, error) {
	if conf == nil {
		conf = DefaultConfig()
	}
	switch name {
	case "mock":
		return NewMock(), nil
	case "redis":
		return newRedis(ctx, conf.URL, conf.Password, conf.Timeout, log)
	default:
		return nil, errors.New("no such store type")
	}
}
