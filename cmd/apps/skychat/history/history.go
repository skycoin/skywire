// Package history provides optional persistence for skychat messages.
//
// By default skychat is entirely ephemeral — messages pass through and are not
// stored anywhere. The history package adds an opt-in BoltDB-backed store with
// strict limits to prevent disk-fill/spam attacks:
//
//   - per-message size cap (drop larger)
//   - per-peer rate limit (token bucket, drops overflow)
//   - per-peer message count cap (FIFO eviction)
//   - total storage cap (reject new writes when full)
//   - TTL expiry (background sweep)
//   - optional whitelist (only persist from known peers)
//
// The ephemeral delivery path is never blocked by the store. Messages that
// can't be persisted are still delivered to the UI SSE stream; only the
// side-effect of durable storage is skipped.
package history

import (
	"errors"
	"fmt"
	"time"
)

// Message is a single chat message record.
type Message struct {
	// Peer is the PK hex of the other party in this conversation (never "self").
	// For incoming messages, Peer == Sender. For outgoing, Peer == Recipient.
	Peer string `json:"peer"`
	// From is the PK hex of who sent the message, or empty for outgoing
	// messages sent by the local visor itself.
	From string `json:"from,omitempty"`
	// Outgoing is true if this was sent by the local visor.
	Outgoing bool `json:"outgoing"`
	// Text is the message body.
	Text string `json:"text"`
	// Timestamp is the server-side receive or send time in UTC.
	Timestamp time.Time `json:"timestamp"`
}

// Store is the persistent chat history backend.
type Store interface {
	// Append stores a message. Returns ErrRateLimited, ErrTooLarge,
	// ErrStorageFull, ErrNotWhitelisted, or a backend error. Callers should
	// treat non-nil errors as "not persisted" but still deliver the message
	// ephemerally.
	Append(msg Message) error

	// ListByPeer returns up to limit most recent messages for a specific peer,
	// newest last. If limit <= 0, returns all.
	ListByPeer(peer string, limit int) ([]Message, error)

	// ListRecent returns up to limit most recent messages across all peers,
	// newest last.
	ListRecent(limit int) ([]Message, error)

	// Peers returns the set of peer PKs that have any stored messages.
	Peers() ([]string, error)

	// Close releases the underlying storage.
	Close() error
}

// Limits configures the anti-abuse guardrails on the persistence layer.
// Defaults (via DefaultLimits) are conservative.
type Limits struct {
	// MaxMessageSize is the maximum byte length of the Text field.
	// Larger messages are rejected with ErrTooLarge.
	MaxMessageSize int

	// PerPeerRatePerMin bounds how many messages per minute per peer will be
	// persisted. Excess messages are dropped with ErrRateLimited. Zero
	// disables rate limiting.
	PerPeerRatePerMin int

	// PerPeerCap is the maximum number of messages stored per peer. When
	// exceeded, oldest messages for that peer are evicted FIFO.
	// Zero disables the per-peer cap.
	PerPeerCap int

	// TotalCapBytes is the soft limit on total persisted data across all
	// peers. When exceeded, new writes are rejected with ErrStorageFull.
	// The store does NOT evict older messages to make room — the operator
	// is expected to raise the cap or prune manually. Zero disables.
	TotalCapBytes int64

	// TTL is how long to keep messages before a background sweep removes
	// them. Zero disables the sweep.
	TTL time.Duration

	// WhitelistOnly, if true, only persists messages where the peer PK is
	// in Whitelist. Other messages are rejected with ErrNotWhitelisted.
	// They still pass through to the ephemeral UI.
	WhitelistOnly bool

	// Whitelist is the set of peer PKs permitted when WhitelistOnly is true.
	// The set is keyed by the PK hex string.
	Whitelist map[string]bool
}

// DefaultLimits returns conservative defaults suitable for a persistent
// skychat running on a personal visor.
func DefaultLimits() Limits {
	return Limits{
		MaxMessageSize:    4 * 1024,         // 4 KB
		PerPeerRatePerMin: 20,               // 20 msgs/min
		PerPeerCap:        500,              // 500 messages per peer
		TotalCapBytes:     10 * 1024 * 1024, // 10 MB
		TTL:               30 * 24 * time.Hour,
		WhitelistOnly:     false,
	}
}

// Validate checks that the limits are self-consistent.
func (l Limits) Validate() error {
	if l.MaxMessageSize < 0 {
		return errors.New("MaxMessageSize must be >= 0")
	}
	if l.PerPeerRatePerMin < 0 {
		return errors.New("PerPeerRatePerMin must be >= 0")
	}
	if l.PerPeerCap < 0 {
		return errors.New("PerPeerCap must be >= 0")
	}
	if l.TotalCapBytes < 0 {
		return errors.New("TotalCapBytes must be >= 0")
	}
	if l.TTL < 0 {
		return errors.New("TTL must be >= 0")
	}
	if l.WhitelistOnly && len(l.Whitelist) == 0 {
		return errors.New("WhitelistOnly is set but Whitelist is empty")
	}
	return nil
}

// Errors returned by Store.Append when a message is rejected by limits.
var (
	ErrRateLimited    = errors.New("per-peer rate limit exceeded")
	ErrTooLarge       = errors.New("message exceeds max size")
	ErrStorageFull    = errors.New("total storage cap reached")
	ErrNotWhitelisted = errors.New("peer not in whitelist")
	ErrEmptyPeer      = errors.New("peer PK is empty")
)

// String returns a one-line representation of the message.
func (m Message) String() string {
	direction := "<-"
	if m.Outgoing {
		direction = "->"
	}
	return fmt.Sprintf("[%s] %s %s %q",
		m.Timestamp.UTC().Format(time.RFC3339),
		direction,
		m.Peer,
		truncate(m.Text, 60),
	)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
