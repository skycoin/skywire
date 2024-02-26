// Package store internal/dmsg-discovery/store/redis.go
package store

import (
	"context"
	"sort"
	"time"

	"github.com/go-redis/redis/v8"
	jsoniter "github.com/json-iterator/go"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/netutil"

	"github.com/skycoin/dmsg/pkg/disc"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
)

var json = jsoniter.ConfigFastest

type redisStore struct {
	client  *redis.Client
	timeout time.Duration
}

func newRedis(ctx context.Context, url, password string, timeout time.Duration, log *logging.Logger) (Storer, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	opt.Password = password

	client := redis.NewClient(opt)

	err = netutil.NewRetrier(log, netutil.DefaultInitBackoff, netutil.DefaultMaxBackoff, 10, netutil.DefaultFactor).Do(ctx, func() error {
		_, err = client.Ping(ctx).Result()
		return err
	})
	if err != nil {
		return nil, err
	}
	return &redisStore{client: client, timeout: timeout}, nil
}

// Entry implements Storer Entry method for redisdb database
func (r *redisStore) Entry(ctx context.Context, staticPubKey cipher.PubKey) (*disc.Entry, error) {
	payload, err := r.client.Get(ctx, staticPubKey.Hex()).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, disc.ErrKeyNotFound
		}

		log.WithError(err).WithField("pk", staticPubKey).Errorf("Failed to get entry from redis")
		return nil, disc.ErrUnexpected
	}

	var entry *disc.Entry
	if err := json.Unmarshal(payload, &entry); err != nil {
		log.WithError(err).Warnf("Failed to unmarshal payload %q", payload)
	}

	return entry, nil
}

// Entry implements Storer Entry method for redisdb database
func (r *redisStore) SetEntry(ctx context.Context, entry *disc.Entry, timeout time.Duration) error {
	payload, err := json.Marshal(entry)
	if err != nil {
		return disc.ErrUnexpected
	}

	if entry.Server != nil {
		timeout = dmsg.DefaultUpdateInterval * 2
	}

	err = r.client.Set(ctx, entry.Static.Hex(), payload, timeout).Err()
	if err != nil {
		log.WithError(err).Errorf("Failed to set entry in redis")
		return disc.ErrUnexpected
	}

	if entry.Server != nil {
		err = r.client.SAdd(ctx, "servers", entry.Static.Hex()).Err()
		if err != nil {
			log.WithError(err).Errorf("Failed to add to servers (SAdd) from redis")
			return disc.ErrUnexpected
		}
	}
	if entry.Client != nil {
		err = r.client.SAdd(ctx, "clients", entry.Static.Hex()).Err()
		if err != nil {
			log.WithError(err).Errorf("Failed to add to clients (SAdd) from redis")
			return disc.ErrUnexpected
		}
	}
	if entry.ClientType == "visor" {
		err = r.client.SAdd(ctx, "visorClients", entry.Static.Hex()).Err()
		if err != nil {
			log.WithError(err).Errorf("Failed to add to visorClients (SAdd) from redis")
			return disc.ErrUnexpected
		}
	}

	return nil
}

// DelEntry implements Storer DelEntry method for redisdb database
func (r *redisStore) DelEntry(ctx context.Context, staticPubKey cipher.PubKey) error {
	err := r.client.Del(ctx, staticPubKey.Hex()).Err()
	if err != nil {
		log.WithError(err).WithField("pk", staticPubKey).Errorf("Failed to delete entry from redis")
		return err
	}
	// Delete pubkey from servers or clients set stored
	r.client.SRem(ctx, "servers", staticPubKey.Hex())
	r.client.SRem(ctx, "clients", staticPubKey.Hex())
	r.client.SRem(ctx, "visorClients", staticPubKey.Hex())
	return nil
}

// AvailableServers implements Storer AvailableServers method for redisdb database
func (r *redisStore) AvailableServers(ctx context.Context, maxCount int) ([]*disc.Entry, error) {
	var entries []*disc.Entry

	pks, err := r.client.SRandMemberN(ctx, "servers", int64(maxCount)).Result()
	if err != nil {
		log.WithError(err).Errorf("Failed to get servers (SRandMemberN) from redis")
		return nil, disc.ErrUnexpected
	}

	if len(pks) == 0 {
		return entries, nil
	}

	payloads, err := r.client.MGet(ctx, pks...).Result()
	if err != nil {
		log.WithError(err).Errorf("Failed to set servers (MGet) from redis")
		return nil, disc.ErrUnexpected
	}

	for _, payload := range payloads {
		// if there's no record for this PK, nil is returned. The below
		// type assertion will panic in this case, so we skip
		if payload == nil {
			continue
		}

		var entry *disc.Entry
		if err := json.Unmarshal([]byte(payload.(string)), &entry); err != nil {
			log.WithError(err).Warnf("Failed to unmarshal payload %s", payload.(string))
			continue
		}

		if entry.Server.AvailableSessions <= 0 {
			log.WithField("server_pk", entry.Static).
				Warn("Server is at max capacity. Skipping...")
			continue
		}

		entries = append(entries, entry)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Server.AvailableSessions > entries[j].Server.AvailableSessions
	})

	return entries, nil
}

// AllServers implements Storer AllServers method for redisdb database
func (r *redisStore) AllServers(ctx context.Context) ([]*disc.Entry, error) {
	var entries []*disc.Entry

	pks, err := r.client.SRandMemberN(ctx, "servers", r.client.SCard(ctx, "servers").Val()).Result()
	if err != nil {
		log.WithError(err).Errorf("Failed to get servers (SRandMemberN) from redis")
		return nil, disc.ErrUnexpected
	}

	if len(pks) == 0 {
		return entries, nil
	}

	payloads, err := r.client.MGet(ctx, pks...).Result()
	if err != nil {
		log.WithError(err).Errorf("Failed to set servers (MGet) from redis")
		return nil, disc.ErrUnexpected
	}

	for _, payload := range payloads {
		// if there's no record for this PK, nil is returned. The below
		// type assertion will panic in this case, so we skip
		if payload == nil {
			continue
		}

		var entry *disc.Entry
		if err := json.Unmarshal([]byte(payload.(string)), &entry); err != nil {
			log.WithError(err).Warnf("Failed to unmarshal payload %s", payload.(string))
			continue
		}

		entries = append(entries, entry)
	}

	return entries, nil
}

func (r *redisStore) CountEntries(ctx context.Context) (int64, int64, error) {
	numberOfServers, err := r.client.SCard(ctx, "servers").Result()
	if err != nil {
		log.WithError(err).Errorf("Failed to get servers count (SCard) from redis")
		return numberOfServers, int64(0), err
	}
	numberOfClients, err := r.client.SCard(ctx, "clients").Result()
	if err != nil {
		log.WithError(err).Errorf("Failed to get clients count (SCard) from redis")
		return numberOfServers, numberOfClients, err
	}

	return numberOfServers, numberOfClients, nil
}

func (r *redisStore) RemoveOldServerEntries(ctx context.Context) error {
	servers, err := r.client.SMembers(ctx, "servers").Result()
	if err != nil {
		return err
	}
	for _, server := range servers {
		if r.client.Exists(ctx, server).Val() == 0 {
			r.client.SRem(ctx, "servers", server)
		}
	}
	return nil
}

func (r *redisStore) AllEntries(ctx context.Context) ([]string, error) {
	clients, err := r.client.SMembers(ctx, "clients").Result()
	if err != nil {
		return nil, err
	}
	return clients, err
}

func (r *redisStore) AllVisorEntries(ctx context.Context) ([]string, error) {
	clients, err := r.client.SMembers(ctx, "visorClients").Result()
	if err != nil {
		return nil, err
	}
	return clients, err
}
