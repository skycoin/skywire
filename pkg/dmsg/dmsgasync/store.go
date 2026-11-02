// Package dmsgasync pkg/dmsg/dmsgasync/store.go c1-net-dmsg
package dmsgasync

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

	"github.com/skycoin/skywire/pkg/cipher"
)

// Limits bounds a host's store. A zero field takes its default.
type Limits struct {
	MaxMessage        int           `json:"max_message,omitempty"`
	MaxPerRecipient   int           `json:"max_per_recipient,omitempty"`
	MaxRecipientBytes int64         `json:"max_recipient_bytes,omitempty"`
	MaxTotal          int64         `json:"max_total_size,omitempty"`
	TTL               time.Duration `json:"ttl,omitempty"`
}

func (l Limits) withDefaults() Limits {
	if l.MaxMessage <= 0 {
		l.MaxMessage = DefaultMaxMessage
	}
	if l.MaxMessage > MaxMessageLimit {
		l.MaxMessage = MaxMessageLimit
	}
	if l.MaxPerRecipient <= 0 {
		l.MaxPerRecipient = DefaultMaxPerRecipient
	}
	if l.MaxRecipientBytes <= 0 {
		l.MaxRecipientBytes = DefaultMaxRecipientBytes
	}
	if l.MaxTotal <= 0 {
		l.MaxTotal = DefaultMaxTotal
	}
	if l.TTL <= 0 {
		l.TTL = DefaultTTL
	}
	return l
}

type stored struct {
	Header
	To  cipher.PubKey `json:"to"`
	Ref string        `json:"ref,omitempty"`
	// payload is held only by the memory store.
	payload []byte
}

// store holds the messages of one host. With a dir, payloads live on disk
// and only headers are kept in memory. Safe for concurrent use.
type store struct {
	lim Limits
	dir string

	mu     sync.Mutex
	byRcpt map[cipher.PubKey]map[string]*stored
	bytes  map[cipher.PubKey]int64
	total  int64
}

func newStore(lim Limits, dir string, now time.Time) (*store, error) {
	s := &store{
		lim:    lim.withDefaults(),
		dir:    dir,
		byRcpt: map[cipher.PubKey]map[string]*stored{},
		bytes:  map[cipher.PubKey]int64{},
	}
	if dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("dmsg-async: store dir: %w", err)
		}
		if err := s.load(now); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func newID(now time.Time) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%020d.%s", now.UnixNano(), hex.EncodeToString(b[:])), nil
}

// validID rejects anything that could name a path outside the store.
func validID(id string) bool {
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

// deposit stores payload from `from` for `to` on port under the recipient's
// policy. A repeat of (from, ref) returns the first id and stores nothing.
func (s *store) deposit(from, to cipher.PubKey, port uint16, ref string, payload []byte, pol Policy, now time.Time) (string, error) {
	if to.Null() || port == 0 {
		return "", ErrBadRequest
	}
	if len(payload) > s.lim.MaxMessage {
		return "", ErrTooBig
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fromCount := 0
	for _, m := range s.byRcpt[to] {
		if m.From != from {
			continue
		}
		if ref != "" && m.Ref == ref {
			return m.ID, nil
		}
		fromCount++
	}
	size := int64(len(payload))
	if fromCount >= pol.perSender() {
		return "", ErrSenderFull
	}
	if len(s.byRcpt[to]) >= s.lim.MaxPerRecipient || s.bytes[to]+size > s.lim.MaxRecipientBytes {
		return "", ErrRecipientFull
	}
	if s.total+size > s.lim.MaxTotal {
		return "", ErrStoreFull
	}
	id, err := newID(now)
	if err != nil {
		return "", err
	}
	m := &stored{
		Header: Header{ID: id, From: from, Port: port, Size: len(payload), Deposited: now.UTC()},
		To:     to,
		Ref:    ref,
	}
	if s.dir == "" {
		m.payload = append([]byte(nil), payload...)
	} else if err := s.write(m, payload); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInternal, err)
	}
	if s.byRcpt[to] == nil {
		s.byRcpt[to] = map[string]*stored{}
	}
	s.byRcpt[to][id] = m
	s.bytes[to] += size
	s.total += size
	return id, nil
}

// fetch returns the oldest messages for `to` on ports, up to the batch
// limits, and whether more wait. Nothing is removed until ack.
func (s *store) fetch(to cipher.PubKey, ports []uint16) ([]Message, bool, error) {
	s.mu.Lock()
	msgs := make([]*stored, 0, len(s.byRcpt[to]))
	for _, m := range s.byRcpt[to] {
		if slices.Contains(ports, m.Port) {
			msgs = append(msgs, m)
		}
	}
	s.mu.Unlock()
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].ID < msgs[j].ID })
	var (
		out   []Message
		bytes int
	)
	for _, m := range msgs {
		if len(out) == fetchBatchCount || len(out) > 0 && bytes+m.Size > fetchBatchBytes {
			return out, true, nil
		}
		payload := m.payload
		if s.dir != "" {
			sm, err := readFile(s.path(m.To, m.ID))
			if err != nil {
				// Acked or swept since the list was taken.
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return out, false, fmt.Errorf("%w: %v", ErrInternal, err)
			}
			payload = sm.payload
		}
		out = append(out, Message{Header: m.Header, Payload: payload})
		bytes += m.Size
	}
	return out, false, nil
}

// ack deletes the messages for `to` that ids name. An id that is not `to`'s
// is ignored.
func (s *store) ack(to cipher.PubKey, ids []string) int {
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

// drop deletes everything waiting for `to`.
func (s *store) drop(to cipher.PubKey) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, m := range s.byRcpt[to] {
		s.removeLocked(m)
		n++
	}
	return n
}

func (s *store) removeLocked(m *stored) {
	delete(s.byRcpt[m.To], m.ID)
	if len(s.byRcpt[m.To]) == 0 {
		delete(s.byRcpt, m.To)
		delete(s.bytes, m.To)
	} else {
		s.bytes[m.To] -= int64(m.Size)
	}
	s.total -= int64(m.Size)
	if s.dir != "" {
		_ = os.Remove(s.path(m.To, m.ID)) //nolint:errcheck,gosec // hex key and a host-made id
	}
}

// sweep deletes messages older than the TTL.
func (s *store) sweep(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, msgs := range s.byRcpt {
		for _, m := range msgs {
			if now.Sub(m.Deposited) > s.lim.TTL {
				s.removeLocked(m)
				n++
			}
		}
	}
	return n
}

// Stats is what a store holds.
type Stats struct {
	Messages   int   `json:"messages"`
	Bytes      int64 `json:"bytes"`
	Recipients int   `json:"recipients"`
}

func (s *store) stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Stats{Bytes: s.total, Recipients: len(s.byRcpt)}
	for _, msgs := range s.byRcpt {
		st.Messages += len(msgs)
	}
	return st
}

// On disk a message is <dir>/<recipient hex>/<id>: a 4-byte header length,
// the JSON header, then the payload, written whole and renamed into place.

func (s *store) path(to cipher.PubKey, id string) string {
	return filepath.Join(s.dir, to.Hex(), id)
}

func (s *store) write(m *stored, payload []byte) error {
	head, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(s.dir, m.To.Hex()), 0o700); err != nil {
		return err
	}
	buf := binary.BigEndian.AppendUint32(nil, uint32(len(head))) //nolint:gosec // a JSON header is small
	buf = append(append(buf, head...), payload...)
	path := s.path(m.To, m.ID)
	if err := os.WriteFile(path+".tmp", buf, 0o600); err != nil { //nolint:gosec // hex key and a host-made id
		return err
	}
	return os.Rename(path+".tmp", path) //nolint:gosec // hex key and a host-made id
}

// load indexes what an earlier run left, dropping expired, half-written and
// unreadable files.
func (s *store) load(now time.Time) error {
	dirs, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	for _, d := range dirs {
		var to cipher.PubKey
		if !d.IsDir() || to.Set(d.Name()) != nil {
			continue
		}
		files, err := os.ReadDir(filepath.Join(s.dir, d.Name()))
		if err != nil {
			return err
		}
		for _, f := range files {
			path := filepath.Join(s.dir, d.Name(), f.Name())
			if !validID(f.Name()) {
				_ = os.Remove(path) //nolint:errcheck,gosec
				continue
			}
			m, err := readFile(path)
			if err != nil || m.To != to || m.ID != f.Name() || now.Sub(m.Deposited) > s.lim.TTL {
				_ = os.Remove(path) //nolint:errcheck,gosec
				continue
			}
			m.payload = nil
			if s.byRcpt[to] == nil {
				s.byRcpt[to] = map[string]*stored{}
			}
			s.byRcpt[to][m.ID] = m
			s.bytes[to] += int64(m.Size)
			s.total += int64(m.Size)
		}
	}
	return nil
}

func readFile(path string) (*stored, error) {
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
	var m stored
	if err := json.Unmarshal(raw[4:4+n], &m); err != nil {
		return nil, err
	}
	m.payload = raw[4+n:]
	if len(m.payload) != m.Size {
		return nil, errors.New("payload size does not match its header")
	}
	return &m, nil
}
