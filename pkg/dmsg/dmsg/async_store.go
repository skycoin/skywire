// Package dmsg pkg/dmsg/dmsg/async_store.go c1-net-dmsg
package dmsg

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

// AsyncStoreConfig turns dmsg-async on for a server and bounds it. A zero
// field takes its default. A negative one removes that bound, which only a
// private deployment that trusts its clients should do, except MaxMessage:
// a fetch carries at least one whole message, so it is capped at 1 MiB.
type AsyncStoreConfig struct {
	// Dir keeps messages on disk so they survive a restart. Empty keeps
	// them in memory only.
	Dir string
	// MaxMessage is the largest payload taken, in bytes.
	MaxMessage int
	// MaxPerRecipient and MaxRecipientBytes bound what waits for one PK.
	MaxPerRecipient   int
	MaxRecipientBytes int64
	// MaxSenderPerHour bounds how many messages one PK may leave in an hour.
	MaxSenderPerHour int
	// MaxTotal bounds the whole store, in bytes.
	MaxTotal int64
	// TTL is how long an unfetched message is kept.
	TTL time.Duration
}

func (c AsyncStoreConfig) withDefaults() AsyncStoreConfig {
	if c.MaxMessage == 0 {
		c.MaxMessage = AsyncDefaultMaxMessage
	}
	if c.MaxMessage < 0 {
		c.MaxMessage = asyncFetchBatchBytes
	}
	if c.MaxPerRecipient == 0 {
		c.MaxPerRecipient = AsyncDefaultPerRecipient
	}
	if c.MaxRecipientBytes == 0 {
		c.MaxRecipientBytes = AsyncDefaultRecipientBytes
	}
	if c.MaxSenderPerHour == 0 {
		c.MaxSenderPerHour = AsyncDefaultSenderPerHour
	}
	if c.MaxTotal == 0 {
		c.MaxTotal = AsyncDefaultMaxTotal
	}
	if c.TTL == 0 {
		c.TTL = AsyncDefaultTTL
	}
	return c
}

// info is what the server advertises in discovery.
func (c AsyncStoreConfig) info() *disc.AsyncInfo {
	ttl := int64(c.TTL / time.Second)
	if c.TTL < 0 {
		ttl = 0
	}
	return &disc.AsyncInfo{MaxMessage: c.MaxMessage, TTL: ttl}
}

type asyncStored struct {
	AsyncHeader
	To      cipher.PubKey `json:"to"`
	payload []byte
}

// asyncStore holds the messages a dmsg-async server keeps. Safe for
// concurrent use.
type asyncStore struct {
	cfg AsyncStoreConfig
	log logrus.FieldLogger

	mu      sync.Mutex
	byRcpt  map[cipher.PubKey]map[string]*asyncStored
	rcptLen map[cipher.PubKey]int64
	total   int64
	sent    map[cipher.PubKey][]time.Time // deposit times in the last hour
}

func newAsyncStore(cfg AsyncStoreConfig, log logrus.FieldLogger) (*asyncStore, error) {
	s := &asyncStore{
		cfg:     cfg.withDefaults(),
		log:     log,
		byRcpt:  map[cipher.PubKey]map[string]*asyncStored{},
		rcptLen: map[cipher.PubKey]int64{},
		sent:    map[cipher.PubKey][]time.Time{},
	}
	if s.cfg.MaxMessage > asyncFetchBatchBytes {
		return nil, fmt.Errorf("dmsg-async: max message %d is over %d", s.cfg.MaxMessage, asyncFetchBatchBytes)
	}
	if s.cfg.Dir != "" {
		if err := os.MkdirAll(s.cfg.Dir, 0o700); err != nil {
			return nil, fmt.Errorf("dmsg-async: store dir: %w", err)
		}
		if err := s.load(time.Now()); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func newAsyncID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d.%s", time.Now().UnixNano(), hex.EncodeToString(b[:])), nil
}

// validAsyncID rejects anything that could name a path outside the store.
func validAsyncID(id string) bool {
	if id == "" || len(id) > 64 || strings.Contains(id, "..") {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r == '.') {
			return false
		}
	}
	return true
}

// deposit stores payload from `from` for `to` on port.
func (s *asyncStore) deposit(from, to cipher.PubKey, port uint16, payload []byte, now time.Time) (string, error) {
	c := s.cfg
	if to.Null() || port == 0 {
		return "", ErrAsyncBadRequest
	}
	if len(payload) > c.MaxMessage {
		return "", ErrAsyncTooBig
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	hits := s.sent[from][:0]
	for _, t := range s.sent[from] {
		if now.Sub(t) < time.Hour {
			hits = append(hits, t)
		}
	}
	s.sent[from] = hits
	if c.MaxSenderPerHour > 0 && len(hits) >= c.MaxSenderPerHour {
		return "", ErrAsyncSenderLimit
	}
	size := int64(len(payload))
	if c.MaxPerRecipient > 0 && len(s.byRcpt[to]) >= c.MaxPerRecipient ||
		c.MaxRecipientBytes > 0 && s.rcptLen[to]+size > c.MaxRecipientBytes {
		return "", ErrAsyncRecipientFull
	}
	if c.MaxTotal > 0 && s.total+size > c.MaxTotal {
		return "", ErrAsyncStoreFull
	}
	id, err := newAsyncID()
	if err != nil {
		return "", err
	}
	m := &asyncStored{
		AsyncHeader: AsyncHeader{ID: id, From: from, Port: port, Time: now, Size: len(payload)},
		To:          to,
		payload:     append([]byte(nil), payload...),
	}
	if s.cfg.Dir != "" {
		if err := s.write(m); err != nil {
			s.log.WithError(err).Warn("dmsg-async: write message")
			return "", ErrAsyncInternal
		}
	}
	if s.byRcpt[to] == nil {
		s.byRcpt[to] = map[string]*asyncStored{}
	}
	s.byRcpt[to][id] = m
	s.rcptLen[to] += size
	s.total += size
	s.sent[from] = append(s.sent[from], now)
	return id, nil
}

// fetch returns the oldest messages waiting for `to` on ports, up to the
// batch limits, and whether more wait. Nothing is removed until ack.
func (s *asyncStore) fetch(to cipher.PubKey, ports []uint16) ([]AsyncMessage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs := make([]*asyncStored, 0, len(s.byRcpt[to]))
	for _, m := range s.byRcpt[to] {
		if slices.Contains(ports, m.Port) {
			msgs = append(msgs, m)
		}
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].ID < msgs[j].ID })
	var (
		out   []AsyncMessage
		bytes int
	)
	for _, m := range msgs {
		if len(out) == asyncFetchBatchCount || len(out) > 0 && bytes+m.Size > asyncFetchBatchBytes {
			return out, true
		}
		out = append(out, AsyncMessage{AsyncHeader: m.AsyncHeader, Payload: m.payload})
		bytes += m.Size
	}
	return out, false
}

// ack deletes the messages for `to` that ids name and reports how many.
// An id that is not `to`'s is ignored.
func (s *asyncStore) ack(to cipher.PubKey, ids []string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, id := range ids {
		if m, ok := s.byRcpt[to][id]; ok {
			s.removeLocked(m)
			n++
		}
	}
	return n
}

func (s *asyncStore) removeLocked(m *asyncStored) {
	delete(s.byRcpt[m.To], m.ID)
	if len(s.byRcpt[m.To]) == 0 {
		delete(s.byRcpt, m.To)
		delete(s.rcptLen, m.To)
	} else {
		s.rcptLen[m.To] -= int64(m.Size)
	}
	s.total -= int64(m.Size)
	if s.cfg.Dir != "" {
		if err := os.Remove(s.path(m.To, m.ID)); err != nil && !errors.Is(err, os.ErrNotExist) { //nolint:gosec // hex PK and a server-made id
			s.log.WithError(err).Warn("dmsg-async: remove message")
		}
	}
}

// sweep deletes messages older than the TTL and reports how many.
func (s *asyncStore) sweep(now time.Time) int {
	if s.cfg.TTL < 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, msgs := range s.byRcpt {
		for _, m := range msgs {
			if now.Sub(m.Time) > s.cfg.TTL {
				s.removeLocked(m)
				n++
			}
		}
	}
	for from, hits := range s.sent {
		if len(hits) == 0 || now.Sub(hits[len(hits)-1]) >= time.Hour {
			delete(s.sent, from)
		}
	}
	return n
}

// stats is what the store holds now.
func (s *asyncStore) stats() (messages int, bytes int64, recipients int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, msgs := range s.byRcpt {
		messages += len(msgs)
	}
	return messages, s.total, len(s.byRcpt)
}

// On disk a message is Dir/<recipient hex>/<id>: a 4-byte header length,
// the JSON header, then the payload. A file is written whole and renamed
// into place, so a crash never leaves half a message.

func (s *asyncStore) path(to cipher.PubKey, id string) string {
	return filepath.Join(s.cfg.Dir, to.Hex(), id)
}

func (s *asyncStore) write(m *asyncStored) error {
	head, err := json.Marshal(m)
	if err != nil {
		return err
	}
	dir := filepath.Join(s.cfg.Dir, m.To.Hex())
	if err := os.MkdirAll(dir, 0o700); err != nil { //nolint:gosec // hex PK under the store dir
		return err
	}
	buf := make([]byte, 4, 4+len(head)+len(m.payload))
	binary.BigEndian.PutUint32(buf, uint32(len(head))) //nolint:gosec // a JSON header is small
	buf = append(append(buf, head...), m.payload...)
	path := s.path(m.To, m.ID)
	if err := os.WriteFile(path+".tmp", buf, 0o600); err != nil { //nolint:gosec // hex PK and a server-made id
		return err
	}
	return os.Rename(path+".tmp", path) //nolint:gosec // hex PK and a server-made id
}

// load reads what an earlier run left, dropping expired and unreadable
// files. Limits are not reapplied: what was taken stays until fetched or
// expired.
func (s *asyncStore) load(now time.Time) error {
	dirs, err := os.ReadDir(s.cfg.Dir)
	if err != nil {
		return err
	}
	for _, d := range dirs {
		var to cipher.PubKey
		if !d.IsDir() || to.Set(d.Name()) != nil {
			continue
		}
		files, err := os.ReadDir(filepath.Join(s.cfg.Dir, d.Name()))
		if err != nil {
			return err
		}
		for _, f := range files {
			path := filepath.Join(s.cfg.Dir, d.Name(), f.Name())
			if strings.HasSuffix(f.Name(), ".tmp") || !validAsyncID(f.Name()) {
				_ = os.Remove(path) //nolint:errcheck,gosec
				continue
			}
			m, err := readAsyncFile(path)
			if err != nil || m.To != to || m.ID != f.Name() || s.cfg.TTL > 0 && now.Sub(m.Time) > s.cfg.TTL {
				if err != nil {
					s.log.WithError(err).WithField("file", path).Warn("dmsg-async: dropped an unreadable message")
				}
				_ = os.Remove(path) //nolint:errcheck,gosec
				continue
			}
			if s.byRcpt[to] == nil {
				s.byRcpt[to] = map[string]*asyncStored{}
			}
			s.byRcpt[to][m.ID] = m
			s.rcptLen[to] += int64(m.Size)
			s.total += int64(m.Size)
		}
	}
	return nil
}

func readAsyncFile(path string) (*asyncStored, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // inside the store dir, name checked
	if err != nil {
		return nil, err
	}
	if len(raw) < 4 {
		return nil, errors.New("short file")
	}
	n := int(binary.BigEndian.Uint32(raw))
	if n > len(raw)-4 {
		return nil, errors.New("header past the end")
	}
	var m asyncStored
	if err := json.Unmarshal(raw[4:4+n], &m); err != nil {
		return nil, err
	}
	m.payload = raw[4+n:]
	if len(m.payload) != m.Size {
		return nil, errors.New("payload size does not match its header")
	}
	return &m, nil
}
