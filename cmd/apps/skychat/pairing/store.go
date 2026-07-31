// Package pairing cmd/apps/skychat/pairing/store.go c4-app-chat
// persistence for chat pairs.
//
// One bucket "pairs" with key = peer PK hex, value = JSON Record.
// Crash-safe by virtue of bolt's single-writer semantics: each Put
// is one transaction, each Resume is a read.
//
// The store is intentionally narrow — Add / Get / List / Delete / Set
// — so the lifecycle code in pair.go can drive policy (e.g. when to
// transition status from "pending" to "active") without the store
// having to know about it.
package pairing

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/skycoin/skywire/pkg/cipher"
)

// Status enumerates a Record's lifecycle.
type Status string

const (
	// StatusPending means we've created our publisher and sent (or
	// are about to send) a pair-invite to the peer; the peer hasn't
	// yet acknowledged.
	StatusPending Status = "pending"

	// StatusActive means both sides have publishers up and are
	// subscribed to each other. Normal operating state.
	StatusActive Status = "active"

	// StatusRevoked means we've torn down the pair from our side
	// (e.g. user removed the contact). The record is kept for audit
	// or potential reactivation; pair.go skips revoked records on
	// Resume.
	StatusRevoked Status = "revoked"
)

// Record is the persisted form of a chat pair.
type Record struct {
	// PeerPK is the partner visor's public key.
	PeerPK cipher.PubKey `json:"peer_pk"`

	// Port is the DMSG port both sides use for this pair. Equal to
	// ComputePairPort(myPK, peerPK). Each side's CXO node listens
	// on this port and hosts both publisher (own feed) and
	// subscriber (peer's feed) roles.
	//
	// Persisted (rather than recomputed every time) so a future
	// reserved-port table change doesn't silently shift live pairs.
	Port uint16 `json:"port"`

	Status        Status    `json:"status"`
	EstablishedAt time.Time `json:"established_at"`
	LastMessageAt time.Time `json:"last_message_at,omitempty"`

	// Ratchet is this pair's forward-secrecy state: our current ratchet
	// keypair, the peer's latest announced key, and the ring of epoch
	// keys that still open history. Nil for a record written before the
	// ratchet existed — Open mints a fresh one in that case.
	//
	// Its secrets are sealed at rest (store_seal.go), so this pointer
	// holds plaintext keys in memory and never on disk.
	Ratchet *RatchetState `json:"ratchet,omitempty"`
}

const pairsBucket = "pairs"

// Store is a bolt-backed pair record store. Safe for concurrent use.
type Store struct {
	db *bolt.DB

	// sealer encrypts the ratchet secrets on the way to disk and opens
	// them on the way back. Every read and every write goes through it —
	// see store_seal.go for the construction and its limits.
	sealer *recordSealer

	mu sync.RWMutex
}

// OpenStore opens (or creates) the bolt file at path. The parent dir
// is created if missing.
//
// sk is the visor's secret key and is REQUIRED: it derives the at-rest
// key for the ratchet secrets this store now holds. Passing the zero key
// is refused rather than silently degraded to plaintext.
func OpenStore(path string, sk cipher.SecKey) (*Store, error) {
	sealer, err := newRecordSealer(sk)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("pairing: mkdir parent: %w", err)
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("pairing: open bolt: %w", err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		_, e := tx.CreateBucketIfNotExists([]byte(pairsBucket))
		return e
	}); err != nil {
		_ = db.Close() //nolint:errcheck
		return nil, fmt.Errorf("pairing: init bucket: %w", err)
	}
	return &Store{db: db, sealer: sealer}, nil
}

// Close releases the bolt file handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Put writes (or replaces) the record for r.PeerPK.
func (s *Store) Put(r Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.PeerPK == (cipher.PubKey{}) {
		return fmt.Errorf("pairing: Put: empty PeerPK")
	}
	body, err := s.sealer.encodeRecord(r)
	if err != nil {
		return fmt.Errorf("pairing: marshal record: %w", err)
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(pairsBucket))
		return b.Put([]byte(r.PeerPK.Hex()), body)
	})
}

// Get returns the record for peerPK and a boolean indicating presence.
func (s *Store) Get(peerPK cipher.PubKey) (Record, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var (
		r     Record
		found bool
	)
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(pairsBucket))
		raw := b.Get([]byte(peerPK.Hex()))
		if raw == nil {
			return nil
		}
		found = true
		var derr error
		r, derr = s.sealer.decodeRecord(raw)
		return derr
	})
	return r, found, err
}

// List returns every persisted record. Order is bolt's bucket-sorted
// order (lexicographic on hex peer PK).
func (s *Store) List() ([]Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Record
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(pairsBucket))
		return b.ForEach(func(_, v []byte) error {
			// A record whose ratchet secrets won't open still lists:
			// decodeRecord returns the metadata alongside the error, and
			// dropping the whole contact because one epoch key is
			// unreadable would be a far worse failure than the one being
			// reported. Resume mints a fresh ratchet for it.
			r, derr := s.sealer.decodeRecord(v)
			if derr != nil && r.PeerPK == (cipher.PubKey{}) {
				return derr
			}
			out = append(out, r)
			return nil
		})
	})
	return out, err
}

// Delete removes the record for peerPK. No-op if absent.
func (s *Store) Delete(peerPK cipher.PubKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(pairsBucket))
		return b.Delete([]byte(peerPK.Hex()))
	})
}

// update is the read-modify-write helper every field setter goes
// through. The whole cycle runs inside one bolt transaction, so two
// concurrent setters can't lose each other's write — and, importantly,
// it decodes and re-encodes through the sealer, so a setter that touches
// one metadata field doesn't strip the ratchet secrets on the way back
// out. Doing this by hand per setter was how the group store originally
// lost key state on a roster write.
func (s *Store) update(peerPK cipher.PubKey, who string, mutate func(*Record)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(pairsBucket))
		raw := b.Get([]byte(peerPK.Hex()))
		if raw == nil {
			return fmt.Errorf("pairing: %s: no record for %s", who, peerPK.Hex())
		}
		r, err := s.sealer.decodeRecord(raw)
		if err != nil && r.PeerPK == (cipher.PubKey{}) {
			return err
		}
		mutate(&r)
		body, err := s.sealer.encodeRecord(r)
		if err != nil {
			return err
		}
		return b.Put([]byte(peerPK.Hex()), body)
	})
}

// SetStatus updates only the Status field of an existing record.
// Returns an error if no record exists for peerPK.
func (s *Store) SetStatus(peerPK cipher.PubKey, status Status) error {
	return s.update(peerPK, "SetStatus", func(r *Record) { r.Status = status })
}

// MarkMessage updates LastMessageAt to the given timestamp without
// touching other fields.
func (s *Store) MarkMessage(peerPK cipher.PubKey, ts time.Time) error {
	return s.update(peerPK, "MarkMessage", func(r *Record) { r.LastMessageAt = ts })
}

// SetRatchet persists a pair's ratchet state.
//
// Called on every ratchet change — a rotation, an accepted peer
// announcement, a newly derived epoch — because losing any of them is
// not cosmetic. A visor that rotated and failed to save comes back
// holding a secret the peer has never seen, so its own sends are
// unopenable and the peer's are undecryptable until both sides rotate
// past the gap.
func (s *Store) SetRatchet(peerPK cipher.PubKey, st RatchetState) error {
	return s.update(peerPK, "SetRatchet", func(r *Record) {
		snap := st
		r.Ratchet = &snap
	})
}
