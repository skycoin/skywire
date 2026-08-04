// Package commands cmd/apps/skychat/commands/forward.go
//
// Forwarded messages. Same shape as reply.go — a small JSON envelope riding
// the ordinary message body — so this needs no new send endpoint and no wire
// version bump, and a reader on an older build sees the forwarded text
// rather than a broken card.
//
// # The privacy rule is in the type, not in the callers
//
// A forward may name the CHANNEL it came from and nothing else. There is no
// field here for the original author's name, public key, or timestamp, and
// that absence is the whole design: a rule enforced by "remember not to fill
// this in" is a rule that survives until the first person forgets, whereas a
// field that does not exist cannot be populated by any future call site.
//
// Why channels are the exception: a channel is a broadcast to an audience
// its author cannot enumerate, published under the channel's identity rather
// than a person's. Naming it when a post travels further is repeating what
// was already public. A DM has exactly one intended reader and a group has a
// roster; in both, the author chose an audience, and forwarding with
// attribution would hand a stranger both the content AND the fact that a
// specific person said it — which is the leak, not the content.
//
// So: From carries a channel's display NAME, never a key. Even for channels
// the identity is deliberately the weaker one — a name is enough to say where
// something came from, and a key would let a forward be used to enumerate who
// is in an audience.
//
// # What it is worth
//
// Nothing here is signed, so "Forwarded from X" is a claim by the forwarder,
// exactly like a pasted quotation. It is rendered as a quiet chip rather than
// a badge for that reason — the same stance PriceHint takes in the group
// package. The value is in the common case (a reader knows where a post came
// from) and the cost of a lie is what it would be anyway if the forwarder had
// simply typed the words.
package commands

import (
	"encoding/json"
	"strings"

	"github.com/skycoin/skywire/pkg/skychat/history"
)

// maxForwardFromLen bounds the origin label on receipt. Long enough for a
// channel name (the group package caps those at 64 too), short enough that a
// forward cannot push a paragraph into the chip above a bubble.
const maxForwardFromLen = 64

// forwardMeta is the forward reference carried in a message body.
//
// Two fields, and there will not be a third for the author. See the package
// comment.
type forwardMeta struct {
	// From is the origin CHANNEL's display name, or "" for a forward whose
	// origin must not be disclosed — a DM or a group. An empty From is the
	// normal case, not a degraded one: it renders as a bare "Forwarded"
	// chip, which tells the reader this is not the forwarder's own writing
	// without telling them anything about who wrote it.
	From string `json:"from,omitempty"`

	// Text is the forwarded body.
	Text string `json:"text"`
}

type forwardEnvelope struct {
	Forward *forwardMeta `json:"skychat_forward"`
}

// encodeForwardText renders a forward as the message body a sender publishes.
func encodeForwardText(m forwardMeta) (string, error) {
	m.From = truncateText(m.From, maxForwardFromLen)
	b, err := json.Marshal(forwardEnvelope{Forward: &m})
	return string(b), err
}

// parseForwardText detects a forward envelope in a message body. Same cheap
// prefix + substring gate as parseReplyText, to keep the JSON parser off
// ordinary chat text.
func parseForwardText(text string) (forwardMeta, bool) {
	t := strings.TrimSpace(text)
	if len(t) == 0 || t[0] != '{' || !strings.Contains(t, "skychat_forward") {
		return forwardMeta{}, false
	}
	var env forwardEnvelope
	if err := json.Unmarshal([]byte(t), &env); err != nil || env.Forward == nil {
		return forwardMeta{}, false
	}
	m := *env.Forward
	// Bounded on the way IN as well as out: the sender is a peer, and a
	// label that arrives 4 KB long is a rendering problem here regardless of
	// what our own encoder does.
	m.From = truncateText(m.From, maxForwardFromLen)
	return m, true
}

// enrichForwardRow adds the additive forward_from field to a message map
// (shared by the DM SSE renderer and the group SSE poller / history). The
// caller sets the visible text key ("message" or "text") to meta.Text — the
// two read paths use different key names, so that stays with the caller.
//
// The key is emitted even when the label is empty, because "this is a
// forward" is itself the fact the browser needs in order to draw the chip;
// an absent key would be indistinguishable from an ordinary message.
func enrichForwardRow(row map[string]any, meta forwardMeta) {
	row["forwarded"] = true
	if meta.From != "" {
		row["forward_from"] = meta.From
	}
}

// enrichForwardMessage rewrites a persisted history.Message in place when its
// body is a forward envelope: the stored raw envelope becomes the clean text
// plus the forward fields the browser reads back on reload.
func enrichForwardMessage(m *history.Message) {
	if meta, ok := parseForwardText(m.Text); ok {
		m.Text = meta.Text
		m.Forwarded = true
		m.ForwardFrom = meta.From
	}
}

// truncateText bounds untrusted display text at n bytes.
func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
