// Package dht pkg/dht/mirror_redis.go
//
// RedisMirror writes DHT entries directly to Redis without running a
// Kademlia node. Used by deployment services (DMSG discovery, TPD, SD)
// that have DisableDHT=true but still need to publish their data into
// the DHT dataset. The DMSG servers' DHT nodes read from the same
// Redis and serve the data to visors over Kademlia.
//
// This avoids running redundant DHT nodes in deployment services while
// ensuring their data is available in the DHT.
package dht

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

// contentCacheMaxAge bounds how long we skip re-publishing an unchanged
// subject. A periodic refresh guards against silent Redis data loss or
// eviction and lets new DHT full-nodes bootstrap eventually. 1h is a
// conservative default; TPD heartbeats run at a higher rate, so the
// vast majority of re-registers still hit the skip path.
const contentCacheMaxAge = time.Hour

// RedisMirror writes entries to Redis in the same key format as
// RedisBackend, without requiring a local DHT node.
type RedisMirror struct {
	backend *RedisBackend
	salt    string
	log     *logging.Logger
	pk      cipher.PubKey
	sk      cipher.SecKey

	cacheMu sync.RWMutex
	cache   map[cipher.PubKey]mirroredContent
}

type mirroredContent struct {
	hash    uint64
	savedAt time.Time
}

// NewRedisMirror creates a mirror that writes directly to Redis.
// salt is the DHT namespace (e.g., "dmsg", "tp", "svc").
// pk/sk are used to sign the DHT entries.
func NewRedisMirror(redisAddr, redisPassword string, redisDB int, salt string, pk cipher.PubKey, sk cipher.SecKey, log *logging.Logger) (*RedisMirror, error) {
	backend, err := NewRedisBackend(redisAddr, redisPassword, redisDB, 0)
	if err != nil {
		return nil, fmt.Errorf("redis mirror: %w", err)
	}
	return &RedisMirror{
		backend: backend,
		salt:    salt,
		log:     log,
		pk:      pk,
		sk:      sk,
		cache:   make(map[cipher.PubKey]mirroredContent),
	}, nil
}

// Mirror publishes an entry to Redis under the subject PK's DHT target key.
// This is the same interface as EntryMirror.Mirror so it can be used as
// a drop-in replacement for SetDHTMirror on deployment service APIs.
func (m *RedisMirror) Mirror(subjectPK cipher.PubKey, entry interface{}, seq uint64) {
	m.MirrorMany([]cipher.PubKey{subjectPK}, entry, seq)
}

// MirrorMany publishes an entry to Redis under multiple subject PKs' DHT
// target keys. The signed payload is identical regardless of target key
// (it depends only on seq, value, and salt), so we sign once and save
// under each target. For a 2-edge transport this cuts the per-register
// secp256k1 cost in half; on TPD under production load the single-sign
// path accounts for the majority of the service's CPU.
//
// Subjects whose last-mirrored payload hash matches the current payload
// (and whose cache entry is fresher than contentCacheMaxAge) are
// skipped: no sign, no save. This is the common case for re-register /
// heartbeat flows where the list of transports for an edge hasn't
// changed since the last publish.
func (m *RedisMirror) MirrorMany(subjectPKs []cipher.PubKey, entry interface{}, seq uint64) {
	if len(subjectPKs) == 0 {
		return
	}
	data, err := json.Marshal(entry)
	if err != nil {
		m.log.WithError(err).Warn("Redis mirror: marshal failed")
		return
	}

	h := fnv.New64a()
	_, _ = h.Write(data)
	payloadHash := h.Sum64()
	now := time.Now()

	targets := m.subjectsToPublish(subjectPKs, payloadHash, now)
	if len(targets) == 0 {
		return
	}

	item := MutableItem{
		V:    data,
		K:    m.pk,
		Salt: []byte(m.salt),
		Seq:  seq,
	}
	if err := item.Sign(m.sk); err != nil {
		m.log.WithError(err).Warn("Redis mirror: sign failed")
		return
	}

	salt := []byte(m.salt)
	for _, pk := range targets {
		target := MutableItemTarget(pk, salt)
		if err := m.backend.Save(target, item); err != nil {
			m.log.WithError(err).Warn("Redis mirror: save failed")
			continue
		}
		m.recordPublished(pk, payloadHash, now)
	}
}

// subjectsToPublish filters out subjects whose cached content hash matches
// the current payload and whose cache entry is still fresh. It returns
// the subset that must actually be signed and written.
func (m *RedisMirror) subjectsToPublish(subjectPKs []cipher.PubKey, payloadHash uint64, now time.Time) []cipher.PubKey {
	out := subjectPKs[:0:0]
	m.cacheMu.RLock()
	defer m.cacheMu.RUnlock()
	for _, pk := range subjectPKs {
		cached, ok := m.cache[pk]
		if ok && cached.hash == payloadHash && now.Sub(cached.savedAt) < contentCacheMaxAge {
			continue
		}
		out = append(out, pk)
	}
	return out
}

func (m *RedisMirror) recordPublished(pk cipher.PubKey, payloadHash uint64, now time.Time) {
	m.cacheMu.Lock()
	m.cache[pk] = mirroredContent{hash: payloadHash, savedAt: now}
	m.cacheMu.Unlock()
}

// Delete removes the DHT target for the given subject PK.
// Used when a mirrored entry is deleted from the source-of-truth store
// (e.g., last transport for an edge was deregistered) so the DHT view
// doesn't carry a stale record indefinitely.
func (m *RedisMirror) Delete(subjectPK cipher.PubKey) {
	target := MutableItemTarget(subjectPK, []byte(m.salt))
	if err := m.backend.Delete(target); err != nil {
		m.log.WithError(err).Warn("Redis mirror: delete failed")
	}
	m.cacheMu.Lock()
	delete(m.cache, subjectPK)
	m.cacheMu.Unlock()
}

// Close closes the Redis connection.
func (m *RedisMirror) Close() error {
	return m.backend.Close()
}
