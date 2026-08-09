// Package history pkg/skychat/history/history.go c4-app-chat
//
// By default skychat is entirely ephemeral — messages pass through and are not
// stored anywhere. The history package adds an opt-in persistent store with
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
//
// Two backends implement Store: BoltStore (durable, BoltDB-backed, native
// only — bbolt does not compile under GOOS=js) lives in store_bolt.go behind
// a `!js` build tag; MemStore (in-memory, all platforms including the wasm
// visor) lives in store_mem.go. The Message/GroupMessage types, the Store
// interface, and Limits are pure Go and shared by both, so this file compiles
// everywhere.
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
	// ID is the stable message id carried on the wire in message.Envelope.ID.
	// It makes a stored message addressable — quoted replies (ReplyTo) and,
	// later, receipts/deletes reference it. omitempty: pre-envelope plain-text
	// messages have no id, so the field is simply absent for them.
	ID string `json:"id,omitempty"`
	// ReplyTo, when set, is the ID of the message this one quotes (a threaded
	// reply), mirroring message.Envelope.ReplyTo. Persisting it means the
	// thread survives a reload / history backfill, not just the live SSE event.
	ReplyTo string `json:"reply_to,omitempty"`

	// File attachment metadata — set only for file messages (Telegram-style
	// "a file is a message"). FileURL is the /files/<name> path for received
	// files so the UI can re-render the thumbnail from history; empty for
	// sent files (the sender keeps no served copy). FileID is the transfer id,
	// stored so a "re-request" (backfill) still works after a reload / on a
	// fresh device — without it the UI has no id to request the bytes by.
	FileID     string `json:"file_id,omitempty"`
	FileName   string `json:"file_name,omitempty"`
	FileSize   int64  `json:"file_size,omitempty"`
	FileStatus string `json:"file_status,omitempty"`
	FileURL    string `json:"file_url,omitempty"`

	// Reply reference — set only when this message quotes another (a
	// {"skychat_reply":...} body). Populated at serve time from the stored
	// envelope; the browser renders these as a quote block above the bubble.
	// Distinct from ReplyTo above: that threads by message id (wire-level),
	// these carry the quoted text itself so a reply still renders when the
	// quoted message isn't in the local history.
	ReplyToSender  string `json:"reply_to_sender,omitempty"`
	ReplyToTS      string `json:"reply_to_ts,omitempty"`
	ReplyToPreview string `json:"reply_to_preview,omitempty"`

	// Forward marks — set only when this message was forwarded from
	// somewhere else (a {"skychat_forward":...} body). Populated at serve
	// time from the stored envelope, the same way the reply fields are.
	//
	// Forwarded is separate from ForwardFrom because the common case has no
	// origin to name: a forward out of a DM or a group carries the content
	// and nothing about where it came from (see cmd/apps/skychat/commands/
	// forward.go), so the flag is the only thing distinguishing it from
	// text the forwarder wrote themselves. ForwardFrom, when present, is a
	// channel's display name — never a public key, and never a person.
	Forwarded   bool   `json:"forwarded,omitempty"`
	ForwardFrom string `json:"forward_from,omitempty"`
}

// GroupMessage is a single group-chat message record. Mirrors Message
// but indexed by group ID + sender PK instead of a single peer PK. Stored
// in a parallel bucket so 1:1 queries don't accidentally see group
// traffic and vice versa.
type GroupMessage struct {
	// GroupID is the group identifier this message belongs to.
	GroupID string `json:"group_id"`
	// SenderPK is the publishing member's PK hex.
	SenderPK string `json:"sender_pk"`
	// Outgoing is true if this visor was the sender.
	Outgoing bool `json:"outgoing"`
	// Text is the message body.
	Text string `json:"text"`
	// Timestamp is the message timestamp (sender-set, UTC).
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

	// DeleteByID erases the stored message with the given envelope ID from
	// peer's conversation, in either direction. Reports whether a record
	// actually matched; an unknown peer or id is not an error (a
	// delete-for-everyone can name a message this side never persisted —
	// persistence may be off, or the record may have aged out).
	//
	// This is the durable half of delete-for-everyone: without it the
	// message survives in the store and comes back the next time a client
	// hydrates from history instead of its local cache.
	DeleteByID(peer, id string) (bool, error)

	// Peers returns the set of peer PKs that have any stored messages.
	Peers() ([]string, error)

	// AppendGroup stores a group message. Same error semantics as Append,
	// but rate-limit / whitelist checks key off GroupID instead of Peer.
	// Errors are non-fatal — the caller still surfaces the message to
	// live listeners; only durable storage is skipped.
	AppendGroup(msg GroupMessage) error

	// ListByGroup returns up to limit most recent messages for a specific
	// group, newest last. If limit <= 0, returns all.
	ListByGroup(groupID string, limit int) ([]GroupMessage, error)

	// ListGroupBefore returns up to limit messages STRICTLY OLDER than
	// before, newest last — the backward page cursor.
	//
	// This is what makes a large room readable at all. ListByGroup can
	// only ever hand back the newest N, so without a way to ask for what
	// came before them a client's options were "the tail" or "everything":
	// a channel with fifty thousand posts had to ship all of them to a
	// joiner before showing anything. With this, a joiner takes the newest
	// chunk immediately and walks backwards as it reads.
	//
	// A zero `before` means "from the newest", so the first page is just
	// ListGroupBefore(id, time.Time{}, n) and a caller needs no special
	// case to start. The bound is exclusive for the same reason
	// ListGroupSince's is: `before` is the oldest message the caller
	// already holds, and returning it again would duplicate a row on every
	// page boundary.
	//
	// Ordering matches ListByGroup (newest last) so a page can be
	// prepended to what the caller has without re-sorting.
	ListGroupBefore(groupID string, before time.Time, limit int) ([]GroupMessage, error)

	// ListGroupSince returns every stored group message whose Timestamp
	// is strictly after `since`, oldest first. Returns nil for an
	// unknown group or for a group with no messages newer than `since`.
	//
	// Used by the gRPC StreamGroupMessages handler to backfill a
	// reconnecting subscriber whose disconnect gap is longer than the
	// in-memory inbox ring can cover. The bbolt key is the message
	// timestamp (see tsKey), so the lookup is a cursor Seek + forward
	// walk — no full-bucket scan even on a long-running group.
	ListGroupSince(groupID string, since time.Time) ([]GroupMessage, error)

	// Groups returns the set of group IDs that have any stored messages.
	Groups() ([]string, error)

	// Import merges an archive exported from another device — the only way
	// chat data moves between a desktop and a phone, since nothing here
	// syncs. It is deliberately NOT Append in a loop:
	//
	//   - The per-peer rate limit does not apply. It exists to stop a peer
	//     filling the disk over the network at 20 messages a minute; an
	//     operator restoring their own archive is not that, and applying it
	//     would silently drop all but a handful of every conversation.
	//   - Records already present are skipped, so importing the same file
	//     twice changes nothing. Identity is the envelope ID where there is
	//     one and (timestamp, direction, text) where there is not.
	//
	// Every other guardrail stands: oversized messages are rejected, the
	// whitelist still filters, the total-bytes cap still stops the write,
	// and the per-peer cap still evicts oldest-first — so an archive larger
	// than the cap lands as its newest N messages rather than as an error.
	//
	// Records older than the retention window are stored and counted in
	// ImportResult.Expiring: the next sweep will remove them, and the
	// operator wants to know that before they wipe the source device.
	Import(msgs []Message, groups []GroupMessage) (ImportResult, error)

	// Close releases the underlying storage.
	Close() error
}

// ImportResult reports what Import did with an archive.
type ImportResult struct {
	// Messages and GroupMessages are the records actually stored.
	Messages      int `json:"messages"`
	GroupMessages int `json:"group_messages"`
	// Duplicates were already present and were skipped.
	Duplicates int `json:"duplicates"`
	// Rejected failed a limit — too large, not whitelisted, or the store
	// is full. They are counted rather than returned as an error: one bad
	// record must not abandon the rest of a migration.
	Rejected int `json:"rejected"`
	// Expiring were stored but already fall outside Limits.TTL, so the
	// next sweep removes them. Non-zero means the operator needs a longer
	// retention window (--persist-ttl) to keep this history.
	Expiring int `json:"expiring"`
	// Evicted were pushed back out by the per-peer cap, which drops
	// oldest-first — so importing an archive larger than the cap keeps its
	// newest end. Counted because "imported 2000" while 500 survive is a
	// number that would send someone to wipe their old device too early.
	Evicted int `json:"evicted"`
}

// messageKey identifies a 1:1 message for de-duplication.
//
// The envelope ID is the real identity and is used wherever it exists. Older
// plain-text messages have none, and for those the timestamp, direction and
// body together are as close as this can get: two distinct messages sharing
// all three in one conversation are indistinguishable to a reader too.
func messageKey(m Message) string {
	if m.ID != "" {
		return "i\x00" + m.ID
	}
	dir := "in"
	if m.Outgoing {
		dir = "out"
	}
	return "t\x00" + m.Timestamp.UTC().Format(time.RFC3339Nano) + "\x00" + dir + "\x00" + m.Text
}

// groupMessageKey identifies a group message for de-duplication. Group
// records carry no ID, so the sender, the timestamp and the body are it.
func groupMessageKey(m GroupMessage) string {
	return m.SenderPK + "\x00" + m.Timestamp.UTC().Format(time.RFC3339Nano) + "\x00" + m.Text
}

// expiryCutoff is the timestamp below which a record is already doomed by the
// TTL sweep. Zero TTL means nothing expires.
func (l Limits) expiryCutoff(now time.Time) (time.Time, bool) {
	if l.TTL <= 0 {
		return time.Time{}, false
	}
	return now.Add(-l.TTL), true
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
