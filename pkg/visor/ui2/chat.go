// Package ui2 pkg/visor/ui2/chat.go c3-vis-core
package ui2

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// The chat tab, as skychat.component.ts: direct messages read from
// skychat's history and sent through its /message endpoint, and the
// visor's group rooms.

// ChatMsg is one direct message.
type ChatMsg struct {
	Peer     string    `json:"peer"`
	From     string    `json:"from"`
	Outgoing bool      `json:"outgoing"`
	Text     string    `json:"text"`
	Message  string    `json:"message"`
	TS       time.Time `json:"timestamp"`
	ID       string    `json:"id"`
	ReplyTo  string    `json:"reply_to"`
	Deleted  bool      `json:"deleted"`
}

// Body is the text, from whichever field skychat filled.
func (m ChatMsg) Body() string {
	if m.Text != "" {
		return m.Text
	}
	return m.Message
}

// Chat is the chat tab.
type Chat struct {
	PK     string
	Remote bool
	Mode   string // "dm" or "groups"
	Err    string

	// Direct messages.
	History bool
	Peers   []string
	Peer    string
	Msgs    []ChatMsg
	Draft   string
	ReplyTo string

	// Groups.
	// PwSet is whether the skychat password gates the standalone page.
	PwSet bool
	PwErr string

	Groups []visorapi.GroupInfo
	Group  *visorapi.GroupInfo
	GMsgs  []visorapi.GroupMessage
	Invite string
}

// ParseChatHistory reads skychat's /history and sorts it oldest first.
func ParseChatHistory(b []byte) ([]ChatMsg, error) {
	var ms []ChatMsg
	if err := json.Unmarshal(b, &ms); err != nil {
		return nil, err
	}
	sort.SliceStable(ms, func(i, j int) bool { return ms[i].TS.Before(ms[j].TS) })
	return ms, nil
}

// ChatOf picks the peers, newest conversation first, and peer's messages.
func ChatOf(c Chat, ms []ChatMsg) Chat {
	seen := map[string]bool{}
	for i := len(ms) - 1; i >= 0; i-- {
		p := ms[i].Peer
		if p != "" && !seen[p] {
			seen[p] = true
			c.Peers = append(c.Peers, p)
		}
	}
	if c.Peer != "" && !seen[c.Peer] {
		c.Peers = append([]string{c.Peer}, c.Peers...)
	}
	for _, m := range ms {
		if m.Peer == c.Peer {
			c.Msgs = append(c.Msgs, m)
		}
	}
	return c
}

// QuoteOf is the start of the message a reply points to, or "".
func (c Chat) QuoteOf(id string) string {
	for _, m := range c.Msgs {
		if m.ID == id {
			b := []rune(m.Body())
			if len(b) > 80 {
				return string(b[:80]) + "…"
			}
			return string(b)
		}
	}
	return ""
}

func chatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("01-02 15:04")
}

func (c Chat) url(rest string) string { return VisorURL(c.PK, "chat") + rest }
