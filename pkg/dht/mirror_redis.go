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

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

// RedisMirror writes entries to Redis in the same key format as
// RedisBackend, without requiring a local DHT node.
type RedisMirror struct {
	backend *RedisBackend
	salt    string
	log     *logging.Logger
	pk      cipher.PubKey
	sk      cipher.SecKey
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
	}, nil
}

// Mirror publishes an entry to Redis under the subject PK's DHT target key.
// This is the same interface as EntryMirror.Mirror so it can be used as
// a drop-in replacement for SetDHTMirror on deployment service APIs.
func (m *RedisMirror) Mirror(subjectPK cipher.PubKey, entry interface{}, seq uint64) {
	data, err := json.Marshal(entry)
	if err != nil {
		m.log.WithError(err).Warn("Redis mirror: marshal failed")
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

	target := MutableItemTarget(subjectPK, []byte(m.salt))
	if err := m.backend.Save(target, item); err != nil {
		m.log.WithError(err).Warn("Redis mirror: save failed")
	}
}

// Close closes the Redis connection.
func (m *RedisMirror) Close() error {
	return m.backend.Close()
}
