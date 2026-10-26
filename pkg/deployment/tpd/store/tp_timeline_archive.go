// Package store pkg/deployment/tpd/store/tp_timeline_archive.go c4-net-discovery
//
// Settled per-transport uptime timelines on disk. The tp-uptime:<id>:<date>:
// timeline bitmaps were kept in redis for eight days, 1.67M keys and 48% of
// the keyspace (audit 2026-10-16), for a 7-day read window. They now leave
// redis 36 h after their last write. Before that, each settled day is written
// once to one file, and the uptime endpoints read days older than yesterday
// from it, so their answers do not change.
//
// A file holds one UTC day: a header, a block index, and blocks of records
// sorted by transport ID, each block deflated on its own so a lookup reads
// only the blocks it needs. A record is the 16-byte ID and the 36-byte
// bitmap exactly as redis held it.
package store

import (
	"bytes"
	"compress/flate"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	timelineArchiveMagic   = "SWTL"
	timelineArchiveVersion = 1
	// timelineRecordSize is one record: transport ID then bitmap.
	timelineRecordSize = 16 + transportTimelineBitmapBytes
	// timelineBlockRecords is how many records one deflated block holds.
	timelineBlockRecords = 1024
	// timelineHeaderSize is magic, version, date, record and block counts.
	timelineHeaderSize = 4 + 1 + 10 + 4 + 4
	// timelineIndexEntrySize is a block's first ID, offset and length.
	timelineIndexEntrySize = 16 + 8 + 4
	// timelineArchiveAfter is how long after a day ends it is archived:
	// late enough that no more bits arrive for it, and well inside the
	// 36 h its keys outlive their last write.
	timelineArchiveAfter = 18 * time.Hour
	// timelineArchiveLiveDays is how many recent days, today included, the
	// readers take from redis rather than the archive.
	timelineArchiveLiveDays = 2
	// timelineScanCount and timelineGetBatch pace the archiving reads.
	timelineScanCount = 10000
	timelineGetBatch  = 1000
)

// SetUptimeArchive keeps settled per-transport uptime timelines in dir.
// Empty disables the archive, and the readers use redis alone.
func (s *redisStore) SetUptimeArchive(dir string) {
	s.uptimeArchive = dir
}

func (s *redisStore) timelineArchiveFile(date string) string {
	return TimelineArchivePath(s.uptimeArchive, date)
}

// TimelineArchivePath is the archive file of date under dir.
func TimelineArchivePath(dir, date string) string {
	return filepath.Join(dir, date+".tptl")
}

// TimelineRecord is one transport's bitmap for a day.
type TimelineRecord struct {
	ID     uuid.UUID
	Bitmap [transportTimelineBitmapBytes]byte
}

// WriteTimelineArchive writes recs as date's archive file at path, replacing
// it atomically. recs is sorted in place.
func WriteTimelineArchive(path, date string, recs []TimelineRecord) error {
	if !validDateKey(date) {
		return fmt.Errorf("timeline archive: invalid date %q", date)
	}
	slices.SortFunc(recs, func(a, b TimelineRecord) int { return bytes.Compare(a.ID[:], b.ID[:]) })
	nblocks := (len(recs) + timelineBlockRecords - 1) / timelineBlockRecords

	var blocks bytes.Buffer
	index := make([]byte, 0, nblocks*timelineIndexEntrySize)
	fw, err := flate.NewWriter(nil, flate.BestCompression)
	if err != nil {
		return err
	}
	base := uint64(timelineHeaderSize + nblocks*timelineIndexEntrySize) //nolint:gosec
	raw := make([]byte, 0, timelineBlockRecords*timelineRecordSize)
	for b := 0; b < nblocks; b++ {
		block := recs[b*timelineBlockRecords : min(len(recs), (b+1)*timelineBlockRecords)]
		raw = raw[:0]
		for _, r := range block {
			raw = append(raw, r.ID[:]...)
			raw = append(raw, r.Bitmap[:]...)
		}
		start := blocks.Len()
		fw.Reset(&blocks)
		if _, err := fw.Write(raw); err != nil {
			return err
		}
		if err := fw.Close(); err != nil {
			return err
		}
		index = append(index, block[0].ID[:]...)
		index = binary.BigEndian.AppendUint64(index, base+uint64(start))         //nolint:gosec
		index = binary.BigEndian.AppendUint32(index, uint32(blocks.Len()-start)) //nolint:gosec
	}

	head := make([]byte, 0, timelineHeaderSize)
	head = append(head, timelineArchiveMagic...)
	head = append(head, timelineArchiveVersion)
	head = append(head, date...)
	head = binary.BigEndian.AppendUint32(head, uint32(len(recs))) //nolint:gosec
	head = binary.BigEndian.AppendUint32(head, uint32(nblocks))   //nolint:gosec

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, date+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck
	for _, p := range [][]byte{head, index, blocks.Bytes()} {
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
	return os.Rename(tmp.Name(), path)
}

// TimelineArchive is an open archive file.
type TimelineArchive struct {
	f       *os.File
	Date    string
	Records int
	index   []timelineBlock
}

type timelineBlock struct {
	first  uuid.UUID
	offset int64
	length int
}

// OpenTimelineArchive opens an archive file and reads its index. It returns
// os.ErrNotExist when there is no file.
func OpenTimelineArchive(path string) (*TimelineArchive, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, err
	}
	a, err := readTimelineIndex(f)
	if err != nil {
		_ = f.Close() //nolint:errcheck
		return nil, fmt.Errorf("timeline archive %s: %w", path, err)
	}
	return a, nil
}

func readTimelineIndex(f *os.File) (*TimelineArchive, error) {
	var head [timelineHeaderSize]byte
	if _, err := io.ReadFull(f, head[:]); err != nil {
		return nil, err
	}
	if string(head[:4]) != timelineArchiveMagic || head[4] != timelineArchiveVersion {
		return nil, errors.New("not a version 1 timeline archive")
	}
	a := &TimelineArchive{f: f, Date: string(head[5:15]),
		Records: int(binary.BigEndian.Uint32(head[15:19]))}
	nblocks := int(binary.BigEndian.Uint32(head[19:23]))
	if nblocks > a.Records {
		return nil, errors.New("corrupt block count")
	}
	idx := make([]byte, nblocks*timelineIndexEntrySize)
	if _, err := io.ReadFull(f, idx); err != nil {
		return nil, err
	}
	a.index = make([]timelineBlock, nblocks)
	for i := range a.index {
		e := idx[i*timelineIndexEntrySize:]
		copy(a.index[i].first[:], e[:16])
		a.index[i].offset = int64(binary.BigEndian.Uint64(e[16:24])) //nolint:gosec
		a.index[i].length = int(binary.BigEndian.Uint32(e[24:28]))
	}
	return a, nil
}

// Close closes the file.
func (a *TimelineArchive) Close() error { return a.f.Close() }

// block returns block i's records, raw.
func (a *TimelineArchive) block(i int) ([]byte, error) {
	b := a.index[i]
	comp := make([]byte, b.length)
	if _, err := a.f.ReadAt(comp, b.offset); err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(flate.NewReader(bytes.NewReader(comp)))
	if err != nil {
		return nil, err
	}
	if len(raw)%timelineRecordSize != 0 {
		return nil, errors.New("corrupt block")
	}
	return raw, nil
}

// Lookup returns the bitmaps of those ids the day holds, reading each block
// it needs once.
func (a *TimelineArchive) Lookup(ids []uuid.UUID) (map[uuid.UUID][]byte, error) {
	sorted := slices.Clone(ids)
	slices.SortFunc(sorted, func(x, y uuid.UUID) int { return bytes.Compare(x[:], y[:]) })
	out := make(map[uuid.UUID][]byte)
	var (
		cur = -1
		raw []byte
	)
	for _, id := range sorted {
		// The last block whose first ID is not after id.
		b := sort.Search(len(a.index), func(i int) bool { return bytes.Compare(a.index[i].first[:], id[:]) > 0 }) - 1
		if b < 0 {
			continue
		}
		if b != cur {
			var err error
			if raw, err = a.block(b); err != nil {
				return nil, err
			}
			cur = b
		}
		n := len(raw) / timelineRecordSize
		j := sort.Search(n, func(i int) bool {
			return bytes.Compare(raw[i*timelineRecordSize:i*timelineRecordSize+16], id[:]) >= 0
		})
		if j < n && bytes.Equal(raw[j*timelineRecordSize:j*timelineRecordSize+16], id[:]) {
			out[id] = raw[j*timelineRecordSize+16 : (j+1)*timelineRecordSize]
		}
	}
	return out, nil
}

// Each calls fn with every record in ID order, stopping at the first error.
func (a *TimelineArchive) Each(fn func(TimelineRecord) error) error {
	for b := range a.index {
		raw, err := a.block(b)
		if err != nil {
			return err
		}
		for off := 0; off < len(raw); off += timelineRecordSize {
			var r TimelineRecord
			copy(r.ID[:], raw[off:off+16])
			copy(r.Bitmap[:], raw[off+16:off+timelineRecordSize])
			if err := fn(r); err != nil {
				return err
			}
		}
	}
	return nil
}

// ArchiveTransportTimelines writes each settled day still in redis that the
// archive lacks. A day is settled timelineArchiveAfter after it ends. Each
// day is read once, with SCAN and batched GETs, off the hot path.
func (s *redisStore) ArchiveTransportTimelines(ctx context.Context, now time.Time) error {
	if s.uptimeArchive == "" {
		return nil
	}
	now = now.UTC()
	today := now.Truncate(24 * time.Hour)
	// Keys written before the 36 h TTL lived eight days, so the first run
	// archives back that far.
	for d := 8; d >= 1; d-- {
		day := today.AddDate(0, 0, -d)
		if now.Before(day.Add(24*time.Hour + timelineArchiveAfter)) {
			continue
		}
		date := day.Format(MetricsDateFormat)
		path := s.timelineArchiveFile(date)
		if _, err := os.Stat(path); err == nil {
			s.archivedTimelineDays.Store(date, struct{}{})
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		recs, err := s.scanTransportTimelines(ctx, date)
		if err != nil {
			return fmt.Errorf("archive timelines %s: %w", date, err)
		}
		if len(recs) == 0 {
			continue // nothing left in redis for that day
		}
		if err := WriteTimelineArchive(path, date, recs); err != nil {
			return fmt.Errorf("archive timelines %s: %w", date, err)
		}
		s.archivedTimelineDays.Store(date, struct{}{})
		s.log.WithField("date", date).WithField("transports", len(recs)).Info("archived per-transport uptime timelines")
	}
	return nil
}

// scanTransportTimelines reads every per-transport timeline of date.
func (s *redisStore) scanTransportTimelines(ctx context.Context, date string) ([]TimelineRecord, error) {
	prefix := serviceName + ":tp-uptime:"
	suffix := ":" + date + ":timeline"
	var (
		recs []TimelineRecord
		keys []string
		ids  []uuid.UUID
	)
	flush := func() error {
		if len(keys) == 0 {
			return nil
		}
		vals, err := s.client.MGet(ctx, keys...).Result()
		if err != nil {
			return err
		}
		for i, v := range vals {
			str, ok := v.(string)
			if !ok || len(str) == 0 {
				continue
			}
			r := TimelineRecord{ID: ids[i]}
			copy(r.Bitmap[:], str)
			recs = append(recs, r)
		}
		keys, ids = keys[:0], ids[:0]
		return nil
	}
	iter := s.client.Scan(ctx, 0, prefix+"*"+suffix, timelineScanCount).Iterator()
	for iter.Next(ctx) {
		k := iter.Val()
		id, err := uuid.Parse(strings.TrimSuffix(strings.TrimPrefix(k, prefix), suffix))
		if err != nil {
			continue
		}
		keys, ids = append(keys, k), append(ids, id)
		if len(keys) == timelineGetBatch {
			if err := flush(); err != nil {
				return nil, err
			}
		}
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return recs, nil
}

// archivedTimelines returns, per date, the archived bitmaps of ids for the
// days of the read window older than timelineArchiveLiveDays. A day not
// archived is absent, and its readers use redis.
func (s *redisStore) archivedTimelines(ids []uuid.UUID, now time.Time) map[string]map[uuid.UUID][]byte {
	if s.uptimeArchive == "" || len(ids) == 0 {
		return nil
	}
	out := make(map[string]map[uuid.UUID][]byte)
	for i := timelineArchiveLiveDays; i < uptimeHistoryDays; i++ {
		date := now.AddDate(0, 0, -i).Format(MetricsDateFormat)
		a, err := OpenTimelineArchive(s.timelineArchiveFile(date))
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				s.log.WithError(err).WithField("date", date).Warn("cannot read archived uptime timelines")
			}
			continue
		}
		got, err := a.Lookup(ids)
		_ = a.Close() //nolint:errcheck
		if err != nil {
			s.log.WithError(err).WithField("date", date).Warn("cannot read archived uptime timelines")
			continue
		}
		out[date] = got
	}
	return out
}

// timelineDayArchived reports whether date is older than yesterday and on
// disk. Visors republish a week of bitmaps on every Root; once a day is
// archived those writes would only keep its keys in redis.
func (s *redisStore) timelineDayArchived(date string, now time.Time) bool {
	if s.uptimeArchive == "" || date >= now.UTC().AddDate(0, 0, -1).Format(MetricsDateFormat) {
		return false
	}
	if _, ok := s.archivedTimelineDays.Load(date); ok {
		return true
	}
	if _, err := os.Stat(s.timelineArchiveFile(date)); err != nil {
		return false
	}
	s.archivedTimelineDays.Store(date, struct{}{})
	return true
}
