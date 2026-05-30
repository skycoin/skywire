// Package serviceuptime — pkg/serviceuptime/store.go: bbolt-backed
// persistence for a service's own uptime / version history.
//
// On-disk layout:
//
//	meta/                  — schema version, instance ID
//	sessions/              — keyed by start_unix_ts (8-byte big-endian);
//	                         value JSON SessionRecord
//	slots/                 — sub-buckets per UTC YYYY-MM-DD; value 36-byte bitmap
//
// Source-of-truth for the running process. A Recorder wraps the Store
// and drives the keepalive loop that turns "process is alive" into
// rows + bits. Anything reading the data (HTTP handler, CLI) goes
// through the Store directly.
package serviceuptime

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"go.etcd.io/bbolt"

	"github.com/skycoin/skywire/pkg/util/bbolthealth"
)

const dateFmt = "2006-01-02"

// SchemaVersion is bumped on any breaking change to bucket layout.
const SchemaVersion = 1

var (
	bucketMeta     = []byte("meta")
	bucketSessions = []byte("sessions")
	bucketSlots    = []byte("slots")

	keySchemaVersion = []byte("schema_version")
	keyInstanceID    = []byte("instance_id")
)

// ErrSchemaMismatch is returned by OpenStore when the persisted schema
// version doesn't match the binary's.
var ErrSchemaMismatch = errors.New("serviceuptime: schema version mismatch")

// SessionRecord is one process incarnation: the span between a binary
// starting and either shutting down cleanly or the next start. The
// recorder writes this row at startup with StartedAt and Version,
// then refreshes LastSeen on every keepalive tick. Gaps between
// adjacent sessions' LastSeen → next StartedAt are "service was not
// running" intervals; tightly-packed sessions with short
// LastSeen-StartedAt durations are restart-loop signal.
type SessionRecord struct {
	StartedAt  time.Time `json:"started_at"`
	LastSeen   time.Time `json:"last_seen"`
	Version    string    `json:"version,omitempty"`
	Commit     string    `json:"commit,omitempty"`
	PID        int       `json:"pid,omitempty"`
	InstanceID string    `json:"instance_id,omitempty"`
	// Service is the human-readable service name ("skywire-visor",
	// "transport-discovery", etc.) — purely informational, useful when
	// rendering history from a copied or shared bbolt file.
	Service string `json:"service,omitempty"`
}

// Duration returns LastSeen − StartedAt, the observed up-time of this
// session. Returns 0 when the row is malformed (LastSeen before start).
func (r SessionRecord) Duration() time.Duration {
	if r.LastSeen.Before(r.StartedAt) {
		return 0
	}
	return r.LastSeen.Sub(r.StartedAt)
}

// Store is the bbolt-backed persistence layer. Safe for concurrent
// use. Paths and TTL are caller-supplied.
type Store struct {
	db         *bbolt.DB
	instanceID string
}

// OpenStore opens (or creates) the bbolt database at path and ensures
// the schema is at the expected version. instanceID is set on first
// open and never rewritten; it identifies the on-disk database
// independently of any single session.
func OpenStore(path string) (*Store, error) {
	// Repair-on-corrupt: bbolt panics inside the internal batch
	// goroutine on a corrupt freelist page, which is unrecoverable
	// from caller code. The local uptime DB is recreatable from
	// runtime state (the visor just loses uptime history for this
	// instance), so renaming a corrupt file aside and starting fresh
	// is strictly safer than crash-looping.
	if err := bbolthealth.RepairIfCorrupt(path); err != nil {
		return nil, fmt.Errorf("serviceuptime: integrity-check %s: %w", path, err)
	}
	db, err := bbolt.Open(path, 0600, &bbolt.Options{
		NoSync:  false,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("serviceuptime: open %s: %w", path, err)
	}
	s := &Store{db: db}
	if err := db.Update(func(tx *bbolt.Tx) error {
		for _, b := range [][]byte{bucketMeta, bucketSessions, bucketSlots} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return fmt.Errorf("create bucket %s: %w", b, err)
			}
		}
		meta := tx.Bucket(bucketMeta)
		raw := meta.Get(keySchemaVersion)
		if raw == nil {
			vbuf := make([]byte, 8)
			binary.BigEndian.PutUint64(vbuf, SchemaVersion)
			if err := meta.Put(keySchemaVersion, vbuf); err != nil {
				return err
			}
		} else if got := binary.BigEndian.Uint64(raw); got != SchemaVersion {
			return fmt.Errorf("%w: on-disk %d, code %d", ErrSchemaMismatch, got, SchemaVersion)
		}
		idRaw := meta.Get(keyInstanceID)
		if idRaw == nil {
			id := newInstanceID()
			if err := meta.Put(keyInstanceID, []byte(id)); err != nil {
				return err
			}
			s.instanceID = id
		} else {
			s.instanceID = string(idRaw)
		}
		return nil
	}); err != nil {
		db.Close() //nolint:errcheck,gosec
		return nil, err
	}
	return s, nil
}

// Close releases the bbolt file handle.
func (s *Store) Close() error { return s.db.Close() }

// InstanceID returns the durable per-database identifier set at first
// open. Useful for distinguishing two copies of the same service that
// share a deployment but differ in their on-disk state.
func (s *Store) InstanceID() string { return s.instanceID }

func sessionKey(t time.Time) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(t.UTC().UnixNano()))
	return buf
}

// PutSession overwrites the row keyed by rec.StartedAt. Callers
// (the Recorder) read-modify-write under their own mutex; the store
// does not merge.
func (s *Store) PutSession(rec SessionRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucketSessions).Put(sessionKey(rec.StartedAt), data)
	})
}

// Sessions returns rows with StartedAt ≥ since, sorted ascending.
// Pass a zero time to read everything in the bucket.
func (s *Store) Sessions(since time.Time) ([]SessionRecord, error) {
	var out []SessionRecord
	err := s.db.View(func(tx *bbolt.Tx) error {
		c := tx.Bucket(bucketSessions).Cursor()
		var seek []byte
		if !since.IsZero() {
			seek = sessionKey(since)
		}
		var k, v []byte
		if seek == nil {
			k, v = c.First()
		} else {
			k, v = c.Seek(seek)
		}
		for ; k != nil; k, v = c.Next() {
			r := SessionRecord{}
			if err := json.Unmarshal(v, &r); err != nil {
				continue
			}
			out = append(out, r)
		}
		return nil
	})
	return out, err
}

// MarkSlot sets the bit for slot in today's bitmap (UTC). The
// read-modify-write happens inside a single bbolt transaction; the OR
// is idempotent so concurrent ticks for the same slot race safely.
func (s *Store) MarkSlot(date time.Time, slot int) error {
	if slot < 0 || slot >= SlotsPerDay {
		return fmt.Errorf("serviceuptime: slot %d out of range", slot)
	}
	dateKey := []byte(date.UTC().Format(dateFmt))
	return s.db.Update(func(tx *bbolt.Tx) error {
		bm := tx.Bucket(bucketSlots).Get(dateKey)
		buf := make([]byte, BitmapSize)
		if bm != nil {
			copy(buf, bm)
		}
		SetSlot(buf, slot)
		return tx.Bucket(bucketSlots).Put(dateKey, buf)
	})
}

// Bitmap returns the persisted bitmap for the given UTC date, or a
// zero-filled bitmap if none exists. The result is a copy.
func (s *Store) Bitmap(date time.Time) ([]byte, error) {
	dateKey := []byte(date.UTC().Format(dateFmt))
	out := make([]byte, BitmapSize)
	err := s.db.View(func(tx *bbolt.Tx) error {
		bm := tx.Bucket(bucketSlots).Get(dateKey)
		if bm != nil {
			copy(out, bm)
		}
		return nil
	})
	return out, err
}

// Dates lists every UTC date that has a stored bitmap, sorted
// ascending.
func (s *Store) Dates() ([]string, error) {
	var dates []string
	err := s.db.View(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucketSlots).ForEach(func(k, _ []byte) error {
			dates = append(dates, string(k))
			return nil
		})
	})
	sort.Strings(dates)
	return dates, err
}

// Prune drops rows older than cutoff: sessions whose LastSeen is
// strictly before cutoff, and slot bitmaps whose date is strictly
// before cutoff's UTC date. Returns sessions removed + bitmaps
// removed.
func (s *Store) Prune(cutoff time.Time) (sessions, bitmaps int, err error) {
	cutoffUTC := cutoff.UTC()
	cutoffDate := cutoffUTC.Format(dateFmt)
	err = s.db.Update(func(tx *bbolt.Tx) error {
		// Sessions: walk and delete by key (which is StartedAt). A
		// session that started before the cutoff but has a fresh
		// LastSeen is preserved — that's a long-running incarnation
		// crossing the retention boundary.
		var sessKeys [][]byte
		if err := tx.Bucket(bucketSessions).ForEach(func(k, v []byte) error {
			r := SessionRecord{}
			if err := json.Unmarshal(v, &r); err == nil {
				if r.LastSeen.UTC().Before(cutoffUTC) {
					sessKeys = append(sessKeys, append([]byte(nil), k...))
				}
			}
			return nil
		}); err != nil {
			return err
		}
		for _, k := range sessKeys {
			if err := tx.Bucket(bucketSessions).Delete(k); err != nil {
				return err
			}
			sessions++
		}
		// Slot bitmaps: keys are YYYY-MM-DD strings; lex order matches
		// chronological order, so a < cutoffDate is "older".
		var dateKeys [][]byte
		if err := tx.Bucket(bucketSlots).ForEach(func(k, _ []byte) error {
			if string(k) < cutoffDate {
				dateKeys = append(dateKeys, append([]byte(nil), k...))
			}
			return nil
		}); err != nil {
			return err
		}
		for _, k := range dateKeys {
			if err := tx.Bucket(bucketSlots).Delete(k); err != nil {
				return err
			}
			bitmaps++
		}
		return nil
	})
	return sessions, bitmaps, err
}

// newInstanceID produces a short opaque identifier for the on-disk
// database. Generated via crypto/rand so two services on the same
// host get distinct IDs even if they happen to start at the same
// nanosecond.
func newInstanceID() string {
	// Inlined to avoid an import cycle if a higher layer wants to
	// depend on this package; crypto/rand failures fall back to a
	// time-based id since the value is informational.
	var buf [12]byte
	if _, err := readRandom(buf[:]); err != nil {
		now := time.Now().UnixNano()
		return fmt.Sprintf("ts-%d", now)
	}
	const hex = "0123456789abcdef"
	out := make([]byte, len(buf)*2)
	for i, b := range buf {
		out[i*2] = hex[b>>4]
		out[i*2+1] = hex[b&0x0f]
	}
	return string(out)
}
