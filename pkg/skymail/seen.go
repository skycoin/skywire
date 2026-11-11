// Package skymail pkg/skymail/seen.go c4-app-mail
package skymail

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// The same message can now arrive more than once: from its sender and
// from a helper carrying it, or again after a reply was lost. The mailbox
// remembers each sender PK and Message-ID it filed and answers a repeat
// as delivered without filing it twice.

// seenFile keeps one "<key> <unix seconds>" line per message filed.
const seenFile = "seen"

// seenTTL is how long a message is remembered, longer than any sender
// or helper keeps retrying with the default limits.
const seenTTL = 30 * 24 * time.Hour

type seenIndex struct {
	path string
	mu   sync.Mutex
	m    map[string]int64
}

func openSeen(dir string) (*seenIndex, error) {
	s := &seenIndex{path: filepath.Join(dir, seenFile), m: map[string]int64{}}
	f, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), " ")
		if t, err := strconv.ParseInt(v, 10, 64); ok && err == nil {
			s.m[k] = t
		}
	}
	return s, sc.Err()
}

// seenKey names one message from one sender. A message with no
// Message-ID has no key and is never taken for a repeat.
func seenKey(sender cipher.PubKey, body []byte) string {
	h, err := parseHeader(body)
	if err != nil {
		return ""
	}
	id := strings.TrimSpace(h.Get("Message-Id"))
	if id == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(id))
	return sender.Hex() + "/" + hex.EncodeToString(sum[:16])
}

// claim marks key as being filed and reports whether it was new. A
// claim that is not followed by keep is undone with release.
func (s *seenIndex) claim(key string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[key]; ok {
		return false
	}
	s.m[key] = now.Unix()
	return true
}

func (s *seenIndex) release(key string) {
	s.mu.Lock()
	delete(s.m, key)
	s.mu.Unlock()
}

// keep writes a claimed key down.
func (s *seenIndex) keep(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // our own file
	if err != nil {
		return err
	}
	_, werr := fmt.Fprintf(f, "%s %d\n", key, s.m[key])
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// prune forgets messages older than seenTTL and rewrites the file.
func (s *seenIndex) prune(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cut := now.Add(-seenTTL).Unix()
	var b strings.Builder
	for k, t := range s.m {
		if t < cut {
			delete(s.m, k)
			continue
		}
		fmt.Fprintf(&b, "%s %d\n", k, t)
	}
	if err := os.WriteFile(s.path+".tmp", []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(s.path+".tmp", s.path)
}
