// Package commands cmd/apps/skychat/commands/reply.go
//
// Quoted replies for DM and group messages. A reply rides the ordinary
// message body as a small JSON envelope — the same reference-in-the-text
// pattern the group-file feature uses ({"skychat_file":...}) — so it needs no
// new send endpoint and no wire-version bump: the sender encodes the envelope,
// and every read boundary (DM /sse, DM /history, group SSE poller, group
// /history) detects it, surfaces the real text as the message body, and adds
// additive reply_to_* fields the browser renders as a quote block.
//
// The reference is denormalized: the parent's sender + timestamp locate it for
// scroll-to, and a short preview is embedded so the quote renders even for a
// reader who doesn't hold the parent (a fresh group joiner, or after backfill).
// Raw non-enriching consumers (e.g. `cli skychat listen` on an old build) see
// the reply's plain text — the same graceful degradation as file messages.
package commands

import (
	"encoding/json"
	"strings"

	"github.com/skycoin/skywire/cmd/apps/skychat/history"
)

// replyMeta is the reply reference carried in a message body.
type replyMeta struct {
	ToSender  string `json:"to_sender,omitempty"`  // parent sender PK hex ("" if unknown)
	ToTS      string `json:"to_ts,omitempty"`      // parent timestamp (RFC3339Nano) for scroll-to
	ToPreview string `json:"to_preview,omitempty"` // short denormalized snippet of the parent
	Text      string `json:"text"`                 // the actual reply body
}

type replyEnvelope struct {
	Reply *replyMeta `json:"skychat_reply"`
}

// encodeReplyText renders a reply as the message body a sender publishes.
func encodeReplyText(m replyMeta) (string, error) {
	b, err := json.Marshal(replyEnvelope{Reply: &m})
	return string(b), err
}

// parseReplyText detects a reply envelope in a message body. A cheap prefix +
// substring gate keeps the JSON parser off ordinary chat text.
func parseReplyText(text string) (replyMeta, bool) {
	t := strings.TrimSpace(text)
	if len(t) == 0 || t[0] != '{' || !strings.Contains(t, "skychat_reply") {
		return replyMeta{}, false
	}
	var env replyEnvelope
	if err := json.Unmarshal([]byte(t), &env); err != nil || env.Reply == nil {
		return replyMeta{}, false
	}
	return *env.Reply, true
}

// enrichReplyRow adds the additive reply_to_* fields to a message map (shared by
// the DM SSE renderer and the group SSE poller / history). The caller sets the
// visible text key ("message" or "text") to meta.Text — the two read paths use
// different key names, so that stays with the caller.
func enrichReplyRow(row map[string]any, meta replyMeta) {
	if meta.ToSender != "" {
		row["reply_to_sender"] = meta.ToSender
	}
	if meta.ToTS != "" {
		row["reply_to_ts"] = meta.ToTS
	}
	if meta.ToPreview != "" {
		row["reply_to_preview"] = meta.ToPreview
	}
}

// enrichReplyMessage rewrites a persisted history.Message in place when its body
// is a reply envelope: the stored raw envelope becomes the clean text plus the
// reply_to_* fields the browser reads back on reload.
func enrichReplyMessage(m *history.Message) {
	if meta, ok := parseReplyText(m.Text); ok {
		m.Text = meta.Text
		m.ReplyToSender = meta.ToSender
		m.ReplyToTS = meta.ToTS
		m.ReplyToPreview = meta.ToPreview
	}
}
