// Package contacts pkg/contacts/store.go c3-vis-core
package contacts

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/emersion/go-vcard"
)

// ErrNotFound is returned for a UID the book does not hold.
var ErrNotFound = errors.New("contact not found")

// Store is an address book kept as one <uid>.vcf file per contact in a
// directory only the visor's user can read: it is the operator's social graph.
type Store struct {
	dir string
	mu  sync.Mutex
}

// OpenStore opens the book in dir, creating it.
func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("contacts: %w", err)
	}
	return &Store{dir: dir}, nil
}

// Dir is where the book is kept.
func (s *Store) Dir() string { return s.dir }

// List returns every contact, sorted by name.
func (s *Store) List() ([]Contact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked()
}

func (s *Store) listLocked() ([]Contact, error) {
	paths, err := filepath.Glob(filepath.Join(s.dir, "*.vcf"))
	if err != nil {
		return nil, err
	}
	out := make([]Contact, 0, len(paths))
	for _, p := range paths {
		data, err := os.ReadFile(p) //nolint:gosec // a file in the book's own directory
		if err != nil {
			return nil, err
		}
		cs, err := decode(bytes.NewReader(data))
		if err != nil || len(cs) != 1 {
			return nil, fmt.Errorf("contacts: %s: want one vCard: %v", filepath.Base(p), err)
		}
		out = append(out, cs[0])
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name); a != b {
			return a < b
		}
		return out[i].UID < out[j].UID
	})
	return out, nil
}

// Put adds c, or replaces the contact with its UID. A contact without a UID
// gets one. It returns c as stored.
func (s *Store) Put(c Contact) (Contact, error) {
	c, err := c.normalized()
	if err != nil {
		return Contact{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return c, s.writeLocked(c)
}

// Delete removes the contact with uid.
func (s *Store) Delete(uid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(s.path(uid))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return err
}

// Import adds every vCard in r, replacing contacts that share a UID. Nothing
// is written unless every card reads.
func (s *Store) Import(r io.Reader) (int, error) {
	cs, err := decode(r)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range cs {
		if err := s.writeLocked(c); err != nil {
			return 0, err
		}
	}
	return len(cs), nil
}

// Export writes the whole book as one .vcf stream.
func (s *Store) Export(w io.Writer) error {
	cs, err := s.List()
	if err != nil {
		return err
	}
	enc := vcard.NewEncoder(w)
	for _, c := range cs {
		if err := enc.Encode(c.card()); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) path(uid string) string {
	return filepath.Join(s.dir, filepath.Base(uid)+".vcf")
}

// writeLocked writes c through a temporary file, so a crash leaves the old
// card or the new one, never half of either.
func (s *Store) writeLocked(c Contact) error {
	var buf bytes.Buffer
	if err := vcard.NewEncoder(&buf).Encode(c.card()); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close() //nolint:errcheck,gosec
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path(c.UID))
}

// decode reads every vCard in r.
func decode(r io.Reader) ([]Contact, error) {
	dec := vcard.NewDecoder(r)
	var out []Contact
	for {
		card, err := dec.Decode()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("contacts: vCard %d: %w", len(out)+1, err)
		}
		c, err := fromCard(card)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
}
