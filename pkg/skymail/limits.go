// Package skymail pkg/skymail/limits.go c4-app-mail
package skymail

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Default limits. The mailbox is on by default and open to every PK, so
// they are low enough that nobody can fill a browser's storage with it:
// a full mailbox refuses mail until expiry or a delete frees room.
const (
	DefaultMaxMessageSize int64 = 1 << 20  // 1 MiB
	DefaultMaxTotalSize   int64 = 16 << 20 // 16 MiB, Inbox and Sent together
	DefaultMaxAge               = 7 * 24 * time.Hour
)

// expiryInterval is how often RunExpiry sweeps.
const expiryInterval = 10 * time.Minute

// ErrMailboxFull is returned when a message does not fit the quota.
var ErrMailboxFull = errors.New("skymail: mailbox full")

// Limits bound what a mailbox keeps. A zero field means its default; a
// negative one removes that bound.
type Limits struct {
	MaxMessageSize int64         `json:"max_message_size"`
	MaxTotalSize   int64         `json:"max_total_size"`
	MaxAge         time.Duration `json:"max_age"`
}

// WithDefaults resolves zero fields to the defaults.
func (l Limits) WithDefaults() Limits {
	if l.MaxMessageSize == 0 {
		l.MaxMessageSize = DefaultMaxMessageSize
	}
	if l.MaxTotalSize == 0 {
		l.MaxTotalSize = DefaultMaxTotalSize
	}
	if l.MaxAge == 0 {
		l.MaxAge = DefaultMaxAge
	}
	return l
}

// limitState holds the effective limits and serializes stores, so the
// quota check and the write that consumes it cannot interleave.
type limitState struct {
	mu      sync.RWMutex
	limits  Limits
	storeMu sync.Mutex
}

// SetLimits replaces the limits; zero fields take their defaults. It
// applies at once: the next session advertises the new size, and a
// lowered MaxAge expires what it now excludes on the next sweep.
func (mb *Mailbox) SetLimits(l Limits) {
	mb.lim.mu.Lock()
	mb.lim.limits = l.WithDefaults()
	mb.lim.mu.Unlock()
}

// Limits returns the effective limits.
func (mb *Mailbox) Limits() Limits {
	mb.lim.mu.RLock()
	defer mb.lim.mu.RUnlock()
	return mb.lim.limits
}

// MaxMessageSize is what the SMTP session advertises and enforces.
func (mb *Mailbox) MaxMessageSize() int64 {
	return mb.Limits().MaxMessageSize
}

// Usage is the bytes held by every folder.
func (mb *Mailbox) Usage() (int64, error) {
	var total int64
	for _, md := range []*maildir{mb.inbox, mb.sent} {
		es, err := md.entries()
		if err != nil {
			return 0, err
		}
		for _, e := range es {
			total += e.size
		}
	}
	return total, nil
}

// store files msg in md if the quota allows it.
func (mb *Mailbox) store(md *maildir, msg []byte, seen bool) (string, error) {
	mb.lim.storeMu.Lock()
	defer mb.lim.storeMu.Unlock()
	l := mb.Limits()
	if l.MaxTotalSize > 0 {
		used, err := mb.Usage()
		if err != nil {
			return "", err
		}
		if used+int64(len(msg)) > l.MaxTotalSize {
			return "", ErrMailboxFull
		}
	}
	return md.put(msg, seen)
}

// full reports whether the quota has no room left at all.
func (mb *Mailbox) full() bool {
	l := mb.Limits()
	if l.MaxTotalSize <= 0 {
		return false
	}
	used, err := mb.Usage()
	return err == nil && used >= l.MaxTotalSize
}

// storedAt is when a message was filed: its unique name begins with
// the delivery time in nanoseconds. Files named otherwise (put there by
// hand) fall back to their modification time.
func storedAt(e entry) time.Time {
	if i := strings.IndexByte(e.id, '.'); i > 0 {
		if n, err := strconv.ParseInt(e.id[:i], 10, 64); err == nil {
			return time.Unix(0, n)
		}
	}
	if fi, err := os.Stat(e.path); err == nil {
		return fi.ModTime()
	}
	return time.Now()
}

// Expire deletes every message older than MaxAge and reports how many.
func (mb *Mailbox) Expire(now time.Time) (int, error) {
	l := mb.Limits()
	if l.MaxAge <= 0 {
		return 0, nil
	}
	n := 0
	for _, md := range []*maildir{mb.inbox, mb.sent} {
		es, err := md.entries()
		if err != nil {
			return n, err
		}
		for _, e := range es {
			if now.Sub(storedAt(e)) <= l.MaxAge {
				continue
			}
			if err := os.Remove(e.path); err != nil && !os.IsNotExist(err) {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

// RunExpiry sweeps now and then every expiryInterval until ctx ends.
func (mb *Mailbox) RunExpiry(ctx context.Context) {
	t := time.NewTicker(expiryInterval)
	defer t.Stop()
	for {
		if n, err := mb.Expire(time.Now()); err != nil {
			mb.log.WithError(err).Warn("skymail: expiry")
		} else if n > 0 {
			mb.log.WithField("removed", n).Info("skymail: expired old mail")
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
