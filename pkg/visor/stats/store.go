// Package stats — pkg/visor/stats/store.go: bbolt-backed Store.
package stats

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"go.etcd.io/bbolt"
)

var (
	bucketMeta       = []byte("meta")
	bucketTransports = []byte("transports")
	bucketTiers      = []byte("tiers")
	bucketServices   = []byte("services")

	keySchemaVersion = []byte("schema_version")
	keyCreatedAt     = []byte("created_at")
)

// dateFmt is the canonical UTC date key used in tier and service
// sub-buckets. Matches the format TPD's redis_uptime.go uses, so a
// future cross-check between the two stores stays trivial.
const dateFmt = "2006-01-02"

// Store is the bbolt-backed persistence layer for the visor's local
// telemetry. Safe for concurrent use; bbolt serializes writes
// internally and the Store wraps reads in its own transactions.
type Store struct {
	db *bbolt.DB
}

// OpenStore opens (or creates) the bbolt database at path and ensures
// the four top-level buckets exist. Schema version is stamped on
// first run; on subsequent opens the version is verified — a mismatch
// returns ErrSchemaMismatch so the caller can decide whether to
// migrate or refuse.
func OpenStore(path string) (*Store, error) {
	db, err := bbolt.Open(path, 0600, &bbolt.Options{
		NoSync:  false,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("stats: open %s: %w", path, err)
	}

	if err := db.Update(func(tx *bbolt.Tx) error {
		for _, b := range [][]byte{bucketMeta, bucketTransports, bucketTiers, bucketServices} {
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
			tbuf, mErr := time.Now().UTC().MarshalBinary()
			if mErr != nil {
				return mErr
			}
			return meta.Put(keyCreatedAt, tbuf)
		}
		got := binary.BigEndian.Uint64(raw)
		if got != SchemaVersion {
			return fmt.Errorf("%w: on-disk %d, code %d", ErrSchemaMismatch, got, SchemaVersion)
		}
		return nil
	}); err != nil {
		db.Close() //nolint:errcheck,gosec
		return nil, err
	}

	return &Store{db: db}, nil
}

// ErrSchemaMismatch is returned by OpenStore when the persisted schema
// version does not match the constant SchemaVersion compiled into
// the binary.
var ErrSchemaMismatch = errors.New("stats: schema version mismatch")

// Close releases the bbolt file handle.
func (s *Store) Close() error {
	return s.db.Close()
}

// PutTransportRecord overwrites the persisted record for a transport.
// Callers (the Tracker) read-modify-write under their own mutex; the
// store does not merge.
func (s *Store) PutTransportRecord(rec *TransportRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucketTransports).Put(rec.ID[:], data)
	})
}

// GetTransportRecord returns the persisted record for a transport, or
// nil if no record has been written yet.
func (s *Store) GetTransportRecord(id uuid.UUID) (*TransportRecord, error) {
	var rec *TransportRecord
	err := s.db.View(func(tx *bbolt.Tx) error {
		raw := tx.Bucket(bucketTransports).Get(id[:])
		if raw == nil {
			return nil
		}
		rec = &TransportRecord{}
		return json.Unmarshal(raw, rec)
	})
	return rec, err
}

// AllTransportRecords returns every persisted transport record. Used
// by the HTTP /stats/transports/history endpoint and by the CXO
// publisher for initial-state hydration.
func (s *Store) AllTransportRecords() ([]*TransportRecord, error) {
	var out []*TransportRecord
	err := s.db.View(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucketTransports).ForEach(func(_, v []byte) error {
			r := &TransportRecord{}
			if err := json.Unmarshal(v, r); err != nil {
				return err
			}
			out = append(out, r)
			return nil
		})
	})
	return out, err
}

// DeleteTransportRecord removes a transport entry. Called by the
// retention sweep for closed transports with no recent activity.
func (s *Store) DeleteTransportRecord(id uuid.UUID) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucketTransports).Delete(id[:])
	})
}

// MarkTierSlot ensures the bitmap key for tier/date exists and sets
// the bit for slot. The read-modify-write happens inside a single
// bbolt transaction, so concurrent ticks for the same tier/date can
// safely race — the OR is idempotent.
func (s *Store) MarkTierSlot(tier string, date time.Time, slot int) error {
	return s.markSlot(bucketTiers, tier, date, slot)
}

// MarkServiceSlot is the per-service analog of MarkTierSlot.
func (s *Store) MarkServiceSlot(service string, date time.Time, slot int) error {
	return s.markSlot(bucketServices, service, date, slot)
}

func (s *Store) markSlot(top []byte, name string, date time.Time, slot int) error {
	if slot < 0 || slot >= SlotsPerDay {
		return fmt.Errorf("stats: slot %d out of range", slot)
	}
	dateKey := []byte(date.UTC().Format(dateFmt))
	return s.db.Update(func(tx *bbolt.Tx) error {
		sub, err := tx.Bucket(top).CreateBucketIfNotExists([]byte(name))
		if err != nil {
			return err
		}
		bm := sub.Get(dateKey)
		buf := make([]byte, BitmapSize)
		if bm != nil {
			copy(buf, bm)
		}
		SetSlot(buf, slot)
		return sub.Put(dateKey, buf)
	})
}

// TierBitmap returns the persisted bitmap for tier on the given UTC
// date, or a zero-filled bitmap if none exists. The result is a copy;
// callers may mutate it freely.
func (s *Store) TierBitmap(tier string, date time.Time) ([]byte, error) {
	return s.getBitmap(bucketTiers, tier, date)
}

// ServiceBitmap is the per-service analog of TierBitmap.
func (s *Store) ServiceBitmap(service string, date time.Time) ([]byte, error) {
	return s.getBitmap(bucketServices, service, date)
}

func (s *Store) getBitmap(top []byte, name string, date time.Time) ([]byte, error) {
	dateKey := []byte(date.UTC().Format(dateFmt))
	out := make([]byte, BitmapSize)
	err := s.db.View(func(tx *bbolt.Tx) error {
		sub := tx.Bucket(top).Bucket([]byte(name))
		if sub == nil {
			return nil
		}
		bm := sub.Get(dateKey)
		if bm != nil {
			copy(out, bm)
		}
		return nil
	})
	return out, err
}

// TierDates lists the UTC dates that have a stored bitmap for tier,
// sorted ascending. Used by query endpoints that need a date range.
func (s *Store) TierDates(tier string) ([]string, error) {
	return s.listDates(bucketTiers, tier)
}

// ServiceDates is the per-service analog of TierDates.
func (s *Store) ServiceDates(service string) ([]string, error) {
	return s.listDates(bucketServices, service)
}

func (s *Store) listDates(top []byte, name string) ([]string, error) {
	var dates []string
	err := s.db.View(func(tx *bbolt.Tx) error {
		sub := tx.Bucket(top).Bucket([]byte(name))
		if sub == nil {
			return nil
		}
		return sub.ForEach(func(k, _ []byte) error {
			dates = append(dates, string(k))
			return nil
		})
	})
	sort.Strings(dates)
	return dates, err
}

// TierNames lists every tier that has any persisted data. Used by
// listing endpoints (`GET /stats/uptime` with no filter returns all).
func (s *Store) TierNames() ([]string, error) {
	return s.listNames(bucketTiers)
}

// ServiceNames is the per-service analog of TierNames.
func (s *Store) ServiceNames() ([]string, error) {
	return s.listNames(bucketServices)
}

func (s *Store) listNames(top []byte) ([]string, error) {
	var names []string
	err := s.db.View(func(tx *bbolt.Tx) error {
		return tx.Bucket(top).ForEach(func(k, _ []byte) error {
			names = append(names, string(k))
			return nil
		})
	})
	sort.Strings(names)
	return names, err
}

// PruneBitmaps deletes tier and service bitmap keys whose date is
// strictly older than cutoff. Returns the number of keys removed.
// Called from the retention sweep at UTC-midnight.
func (s *Store) PruneBitmaps(cutoff time.Time) (int, error) {
	cutoffKey := cutoff.UTC().Format(dateFmt)
	removed := 0
	err := s.db.Update(func(tx *bbolt.Tx) error {
		for _, top := range [][]byte{bucketTiers, bucketServices} {
			b := tx.Bucket(top)
			if err := b.ForEach(func(name, _ []byte) error {
				sub := b.Bucket(name)
				if sub == nil {
					return nil
				}
				var toDel [][]byte
				if err := sub.ForEach(func(k, _ []byte) error {
					if string(k) < cutoffKey {
						toDel = append(toDel, append([]byte(nil), k...))
					}
					return nil
				}); err != nil {
					return err
				}
				for _, k := range toDel {
					if err := sub.Delete(k); err != nil {
						return err
					}
					removed++
				}
				return nil
			}); err != nil {
				return err
			}
		}
		return nil
	})
	return removed, err
}
