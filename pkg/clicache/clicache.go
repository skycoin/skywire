// Package clicache provides a single-file bbolt-backed cache that
// `skywire cli` commands use to memoize deployment-service fetches
// (SD `/api/services`, TPD `/all-transports`, UT `/uptimes`, etc.)
// for the CLI's --cfa minutes-old window.
//
// Replaces the older per-URL JSON-file cache in `/tmp/<service>/...`,
// which had two problems:
//
//   - umask silently downgraded the intended 0o777/0o666 modes, so a
//     CLI run as one user couldn't write into a dir created by a CLI
//     run as another user (typically root via the visor service).
//   - Each cached endpoint was its own file on disk; no way to scope
//     or evict in one operation.
//
// The bbolt approach: one DB file at
// $XDG_CACHE_HOME/skywire/cli-fetch.db (default
// ~/.cache/skywire/cli-fetch.db). System-wide installs that want a
// shared cache across users can set SKYWIRE_CLI_CACHE_DB to
// /var/cache/skywire/cli-fetch.db (or similar) and ensure the file
// is 0o666 with a 0o777 parent — both are set explicitly by Open.
//
// Entries are keyed by URL inside a per-service-host bucket so a
// future cache-clear can target a single service. Values are framed
// as JSON `{"body": "...", "fetched_at": "..."}` so a `bbolt dump`
// or `strings(1)` produces something legible.
package clicache

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/skycoin/skywire/pkg/util/bbolthealth"
)

// DefaultFilename is the bbolt file's basename under the cache dir.
const DefaultFilename = "cli-fetch.db"

// Entry is the value stored under each URL key.
type Entry struct {
	Body      []byte    `json:"body"`
	FetchedAt time.Time `json:"fetched_at"`
}

// Cache is a thin wrapper around a bbolt.DB scoped to the CLI's
// service-fetch use case. The zero value is unusable — use Open.
type Cache struct {
	mu   sync.Mutex
	db   *bolt.DB
	path string
}

// DefaultPath returns the path the CLI should use for its cache db,
// resolved from (in order): $SKYWIRE_CLI_CACHE_DB,
// $XDG_CACHE_HOME/skywire/<DefaultFilename>,
// $HOME/.cache/skywire/<DefaultFilename>.
func DefaultPath() (string, error) {
	if env := os.Getenv("SKYWIRE_CLI_CACHE_DB"); env != "" {
		return env, nil
	}
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "skywire", DefaultFilename), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate user cache dir: %w", err)
	}
	return filepath.Join(home, ".cache", "skywire", DefaultFilename), nil
}

// Open creates or opens the cache DB at path. The parent directory
// is created if missing. Both the directory and the DB file are
// explicitly chmodded to 0o777 / 0o666 so a root-owned DB remains
// writable by other users — this is what the older per-URL file
// cache failed to do (umask downgrade).
//
// The wide permissions are deliberate: when the visor runs as root
// (e.g. via the systemd service) and the operator runs the CLI as
// their own user, both must be able to write the cache or the CLI
// degenerates back into per-user files. The cache contents are
// public deployment-service responses, so wide read perms aren't a
// disclosure risk. gosec G301/G302 warnings are silenced for the
// same reason.
//
// A 2s open timeout matches the rest of the codebase's bbolt usage
// and prevents the CLI from hanging if another process holds the
// lock.
func Open(path string) (*Cache, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o777); err != nil { //nolint:gosec // intentional shared-cache mode; see Open doc
		return nil, fmt.Errorf("clicache: mkdir %q: %w", dir, err)
	}
	// Defeat the umask explicitly — MkdirAll only sets perms on
	// newly-created components, and the second argument is masked
	// by the calling process's umask. Chmod isn't subject to umask.
	if err := os.Chmod(dir, 0o777); err != nil { //nolint:gosec // intentional shared-cache mode; see Open doc
		// Non-fatal: a non-shared cache still works for the calling user.
		_ = err //nolint:errcheck // intentionally swallowed
	}

	// Repair-on-corrupt: clicache is a pure performance cache —
	// recreating it fresh costs only the next fetch's HTTP round trip.
	if err := bbolthealth.RepairIfCorrupt(path); err != nil {
		return nil, fmt.Errorf("clicache: integrity-check %q: %w", path, err)
	}
	db, err := bolt.Open(path, 0o666, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("clicache: open %q: %w", path, err)
	}
	// Same umask-defeat for the file itself.
	_ = os.Chmod(path, 0o666) //nolint:errcheck,gosec // non-fatal; intentional shared-cache mode

	return &Cache{db: db, path: path}, nil
}

// Close releases the underlying bbolt DB. Safe to call on a nil
// receiver so callers can `defer c.Close()` without nil-checks.
func (c *Cache) Close() error {
	if c == nil || c.db == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.db.Close()
	c.db = nil
	return err
}

// Path returns the on-disk location of the DB.
func (c *Cache) Path() string {
	if c == nil {
		return ""
	}
	return c.path
}

// bucketForURL groups entries by the URL's host so a future
// per-service eviction can target a single bucket. Falls back to a
// shared "_" bucket if url.Parse fails — better to cache than to
// drop on the floor.
func bucketForURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "_"
	}
	return u.Host
}

// Get returns the cached entry for rawURL, or ok=false on miss.
// Returns ok=false on any decode error — caller treats it as a
// fresh-fetch trigger rather than propagating a partial value.
func (c *Cache) Get(rawURL string) (Entry, bool) {
	if c == nil || c.db == nil {
		return Entry{}, false
	}
	var e Entry
	hit := false
	err := c.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketForURL(rawURL)))
		if b == nil {
			return nil
		}
		raw := b.Get([]byte(rawURL))
		if raw == nil {
			return nil
		}
		if err := json.Unmarshal(raw, &e); err != nil {
			return nil
		}
		hit = true
		return nil
	})
	if err != nil {
		return Entry{}, false
	}
	return e, hit
}

// Fresh is a convenience wrapper that returns the entry only if
// it's been fetched within maxAge. Equivalent to Get + age check.
func (c *Cache) Fresh(rawURL string, maxAge time.Duration) (Entry, bool) {
	e, ok := c.Get(rawURL)
	if !ok {
		return Entry{}, false
	}
	if time.Since(e.FetchedAt) > maxAge {
		return Entry{}, false
	}
	return e, true
}

// Put stores body under rawURL with the current time as the
// fetched_at timestamp.
func (c *Cache) Put(rawURL string, body []byte) error {
	if c == nil || c.db == nil {
		return errors.New("clicache: nil cache")
	}
	e := Entry{Body: append([]byte(nil), body...), FetchedAt: time.Now().UTC()}
	raw, err := json.Marshal(&e)
	if err != nil {
		return fmt.Errorf("clicache: marshal entry: %w", err)
	}
	return c.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(bucketForURL(rawURL)))
		if err != nil {
			return err
		}
		return b.Put([]byte(rawURL), raw)
	})
}
