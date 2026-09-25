// Package skymail pkg/skymail/maildir.go c4-app-mail
//
// Package skymail is a mailbox a visor hosts for its own PK: mail to
// <anything>@<base32-pk>.skynet (or .dmsg) arrives over plain SMTP on
// port 25, is stamped with the sender's authenticated PK, and is kept
// verbatim in a Maildir. Nothing about it needs a certificate, a domain
// or a public address, so it runs as well in a browser tab as on a
// server. The SMTP protocol loop and the relay are shared with
// pkg/skymailbridge, which is what a host already running Postfix uses.
package skymail

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Folder names. Inbox is the Maildir root; others are Maildir++
// subfolders ("." + name), which is what Dovecot or mutt would open.
const (
	FolderInbox = "INBOX"
	FolderSent  = "Sent"
)

// ErrNotFound is returned for a message id that is not in the folder.
var ErrNotFound = errors.New("skymail: no such message")

// infoSep separates a Maildir unique name from its flags. ':' is the
// standard; Windows forbids it in file names, where '!' is the common
// substitute.
var infoSep = func() string {
	if runtime.GOOS == "windows" {
		return "!"
	}
	return ":"
}()

// maildir is one Maildir folder (tmp/new/cur).
type maildir struct {
	dir string
	mu  sync.Mutex
}

func openMaildir(dir string) (*maildir, error) {
	for _, sub := range []string{"tmp", "new", "cur"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			return nil, fmt.Errorf("skymail: create maildir %s: %w", dir, err)
		}
	}
	return &maildir{dir: dir}, nil
}

// newID returns a Maildir unique name: time, randomness, and a fixed
// host part (the visor's PK is in the headers; it has no place here).
func newID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d.M%s.skymail", time.Now().UnixNano(), hex.EncodeToString(b[:])), nil
}

// validID rejects anything that could name a path outside the folder.
func validID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '.') {
			return false
		}
	}
	return !strings.Contains(id, "..")
}

// put stores msg: written whole into tmp, then renamed into place, so a
// reader (or a snapshot of the filesystem) never sees half a message.
// seen files the message straight into cur, as a Sent copy should be.
func (m *maildir) put(msg []byte, seen bool) (string, error) {
	id, err := newID()
	if err != nil {
		return "", err
	}
	tmp := filepath.Join(m.dir, "tmp", id)
	if err := os.WriteFile(tmp, msg, 0o600); err != nil {
		return "", fmt.Errorf("skymail: write: %w", err)
	}
	dst := filepath.Join(m.dir, "new", id)
	if seen {
		dst = filepath.Join(m.dir, "cur", id+infoSep+"2,S")
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp) //nolint:errcheck,gosec
		return "", fmt.Errorf("skymail: deliver: %w", err)
	}
	return id, nil
}

// entry is one message file as found on disk.
type entry struct {
	id    string
	path  string
	flags string
	size  int64
}

func (m *maildir) entries() ([]entry, error) {
	var out []entry
	for _, sub := range []string{"new", "cur"} {
		des, err := os.ReadDir(filepath.Join(m.dir, sub))
		if err != nil {
			return nil, err
		}
		for _, de := range des {
			if de.IsDir() {
				continue
			}
			name := de.Name()
			id, flags := name, ""
			if i := strings.Index(name, infoSep); i >= 0 {
				id = name[:i]
				if j := strings.Index(name[i:], ","); j >= 0 {
					flags = name[i+j+1:]
				}
			}
			var size int64
			if fi, err := de.Info(); err == nil {
				size = fi.Size()
			}
			out = append(out, entry{id: id, path: filepath.Join(m.dir, sub, name), flags: flags, size: size})
		}
	}
	// Unique names begin with the delivery time in nanoseconds.
	sort.Slice(out, func(i, j int) bool { return out[i].id > out[j].id })
	return out, nil
}

func (m *maildir) find(id string) (entry, error) {
	if !validID(id) {
		return entry{}, ErrNotFound
	}
	es, err := m.entries()
	if err != nil {
		return entry{}, err
	}
	for _, e := range es {
		if e.id == id {
			return e, nil
		}
	}
	return entry{}, ErrNotFound
}

func (m *maildir) read(id string) ([]byte, error) {
	e, err := m.find(id)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(e.path)
}

// markSeen moves a message into cur with the S flag, as any Maildir
// reader does once it has been shown.
func (m *maildir) markSeen(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, err := m.find(id)
	if err != nil {
		return err
	}
	if strings.Contains(e.flags, "S") {
		return nil
	}
	flags := []byte(e.flags + "S")
	sort.Slice(flags, func(i, j int) bool { return flags[i] < flags[j] })
	return os.Rename(e.path, filepath.Join(m.dir, "cur", id+infoSep+"2,"+string(flags)))
}

func (m *maildir) remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, err := m.find(id)
	if err != nil {
		return err
	}
	return os.Remove(e.path)
}

// headLimit is how much of a message List reads to find its headers.
const headLimit = 64 << 10

func readHead(path string) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // path comes from the folder listing
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck
	return io.ReadAll(io.LimitReader(f, headLimit))
}
