// Package store pkg/deployment/tpd/store/leaf_archive.go c4-net-discovery
//
// Settled metrics days on disk. A settled day's leaf (the gzipped
// per-transport records the metrics feed publishes) is the only copy of that
// day's per-transport history once its bw:daily hashes are cleaned up, and at
// ~21 MB a day it was all kept in redis. Each leaf is now also written to the
// archive directory, and redis keeps only the last leafLiveDays of them.
package store

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// leafLiveDays is how many recent settled days keep their leaf in redis.
const leafLiveDays = 14

// SetLeafArchive keeps settled metrics leaves in dir as well as in redis, and
// lets leaves older than leafLiveDays leave redis. Empty keeps them in redis.
func (s *redisStore) SetLeafArchive(dir string) {
	s.leafArchive = dir
}

func (s *redisStore) leafFile(date string) string {
	return filepath.Join(s.leafArchive, date+".leaf")
}

// writeLeafFile stores a day's parts, each length-prefixed, replacing the
// file atomically so a reader never sees half of one.
func (s *redisStore) writeLeafFile(date string, parts [][]byte) error {
	if err := os.MkdirAll(s.leafArchive, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.leafArchive, date+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck
	var n [4]byte
	for _, p := range parts {
		binary.BigEndian.PutUint32(n[:], uint32(len(p))) //nolint:gosec
		if _, err := tmp.Write(n[:]); err != nil {
			_ = tmp.Close() //nolint:errcheck
			return err
		}
		if _, err := tmp.Write(p); err != nil {
			_ = tmp.Close() //nolint:errcheck
			return err
		}
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close() //nolint:errcheck
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.leafFile(date))
}

// readLeafFile returns a day's archived parts, or nil when there is none.
func (s *redisStore) readLeafFile(date string) ([][]byte, error) {
	f, err := os.Open(s.leafFile(date))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck
	var parts [][]byte
	var n [4]byte
	for {
		if _, err := io.ReadFull(f, n[:]); err == io.EOF {
			return parts, nil
		} else if err != nil {
			return nil, fmt.Errorf("leaf archive %s: %w", date, err)
		}
		p := make([]byte, binary.BigEndian.Uint32(n[:]))
		if _, err := io.ReadFull(f, p); err != nil {
			return nil, fmt.Errorf("leaf archive %s: %w", date, err)
		}
		parts = append(parts, p)
	}
}

// archiveLeaves writes every leaf redis holds that the archive lacks, then
// drops from redis the leaves older than leafLiveDays that are safely on disk.
func (s *redisStore) archiveLeaves(ctx context.Context, now time.Time) error {
	if s.leafArchive == "" {
		return nil
	}
	prefix := s.metricsLeafKey("")
	cutoff := now.AddDate(0, 0, -leafLiveDays).Format(MetricsDateFormat)
	iter := s.client.Scan(ctx, 0, prefix+"*", 1000).Iterator()
	var dates []string
	for iter.Next(ctx) {
		dates = append(dates, strings.TrimPrefix(iter.Val(), prefix))
	}
	if err := iter.Err(); err != nil {
		return err
	}
	for _, date := range dates {
		if _, err := os.Stat(s.leafFile(date)); errors.Is(err, os.ErrNotExist) {
			got, err := s.loadLeavesFromRedis(ctx, []string{date})
			if err != nil {
				return err
			}
			parts, ok := got[date]
			if !ok {
				continue
			}
			if err := s.writeLeafFile(date, parts); err != nil {
				return fmt.Errorf("archive leaf %s: %w", date, err)
			}
		} else if err != nil {
			return err
		}
		if date < cutoff {
			if err := s.client.Del(ctx, s.metricsLeafKey(date)).Err(); err != nil {
				return err
			}
		}
	}
	return nil
}
