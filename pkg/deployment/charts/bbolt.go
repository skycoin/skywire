package charts

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"time"

	"github.com/0magnet/bbolt"
)

var (
	bucketRaw    = []byte("raw")
	bucketHourly = []byte("1h")
	bucketStarts = []byte("starts")
	keyRolled    = []byte("rolled")
)

// NewBboltStore returns a Store kept in the bucket named name of db, which
// the caller owns and closes.
func NewBboltStore(db *bbolt.DB, name string) (Store, error) {
	b := &bboltBackend{db: db, name: []byte(name)}
	err := db.Update(func(tx *bbolt.Tx) error {
		top, err := tx.CreateBucketIfNotExists(b.name)
		if err != nil {
			return err
		}
		for _, n := range [][]byte{bucketRaw, bucketHourly, bucketStarts} {
			if _, err := top.CreateBucketIfNotExists(n); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &store{b: b}, nil
}

type bboltBackend struct {
	db   *bbolt.DB
	name []byte
}

func timeKey(t time.Time) []byte {
	k := make([]byte, 8)
	binary.BigEndian.PutUint64(k, uint64(t.Unix())) //nolint:gosec // samples are after 1970, so the order holds
	return k
}

func keyTime(k []byte) time.Time {
	return time.Unix(int64(binary.BigEndian.Uint64(k)), 0).UTC() //nolint:gosec // written by timeKey
}

func (b *bboltBackend) bucket(tx *bbolt.Tx, name []byte) *bbolt.Bucket {
	return tx.Bucket(b.name).Bucket(name)
}

func samplesBucket(hourly bool) []byte {
	if hourly {
		return bucketHourly
	}
	return bucketRaw
}

func (b *bboltBackend) add(_ context.Context, hourly bool, s Sample) error {
	v, err := json.Marshal(s.V)
	if err != nil {
		return err
	}
	return b.db.Update(func(tx *bbolt.Tx) error {
		return b.bucket(tx, samplesBucket(hourly)).Put(timeKey(s.At), v)
	})
}

// each calls fn for every key in [from, to) of the bucket, oldest first.
func (b *bboltBackend) each(name []byte, from, to time.Time, fn func(at time.Time, v []byte)) error {
	end := timeKey(to)
	return b.db.View(func(tx *bbolt.Tx) error {
		c := b.bucket(tx, name).Cursor()
		for k, v := c.Seek(timeKey(from)); k != nil && bytes.Compare(k, end) < 0; k, v = c.Next() {
			fn(keyTime(k), v)
		}
		return nil
	})
}

func (b *bboltBackend) rangeOf(_ context.Context, hourly bool, from, to time.Time) ([]Sample, error) {
	var out []Sample
	err := b.each(samplesBucket(hourly), from, to, func(at time.Time, v []byte) {
		s := Sample{At: at}
		if json.Unmarshal(v, &s.V) == nil {
			out = append(out, s)
		}
	})
	return out, err
}

// trimBucket deletes every key before the given time.
func (b *bboltBackend) trimBucket(name []byte, before time.Time) error {
	end := timeKey(before)
	return b.db.Update(func(tx *bbolt.Tx) error {
		c := b.bucket(tx, name).Cursor()
		for k, _ := c.First(); k != nil && bytes.Compare(k, end) < 0; k, _ = c.First() {
			if err := c.Delete(); err != nil {
				return err
			}
		}
		return nil
	})
}

func (b *bboltBackend) trim(_ context.Context, hourly bool, before time.Time) error {
	return b.trimBucket(samplesBucket(hourly), before)
}

func (b *bboltBackend) rolled(context.Context) (time.Time, error) {
	var at time.Time
	err := b.db.View(func(tx *bbolt.Tx) error {
		if v := tx.Bucket(b.name).Get(keyRolled); len(v) == 8 {
			at = keyTime(v)
		}
		return nil
	})
	return at, err
}

func (b *bboltBackend) setRolled(_ context.Context, h time.Time) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(b.name).Put(keyRolled, timeKey(h))
	})
}

func (b *bboltBackend) addStart(_ context.Context, s Start) error {
	v, err := json.Marshal(wireStart{T: s.At.Unix(), Version: s.Version, Commit: s.Commit})
	if err != nil {
		return err
	}
	return b.db.Update(func(tx *bbolt.Tx) error {
		return b.bucket(tx, bucketStarts).Put(timeKey(s.At), v)
	})
}

func (b *bboltBackend) startsOf(_ context.Context, from, to time.Time) ([]Start, error) {
	var out []Start
	err := b.each(bucketStarts, from, to, func(at time.Time, v []byte) {
		var w wireStart
		if json.Unmarshal(v, &w) == nil {
			out = append(out, Start{At: at, Version: w.Version, Commit: w.Commit})
		}
	})
	return out, err
}

func (b *bboltBackend) trimStarts(_ context.Context, before time.Time) error {
	return b.trimBucket(bucketStarts, before)
}
