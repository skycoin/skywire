package commands

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/skychat/group"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

const (
	// searchLimit caps the hits one search returns, newest first.
	searchLimit = 100
	// searchGroupDepth is how many of each group's newest messages are searched.
	searchGroupDepth = 1000
)

// searchHit is one stored message that matched: a direct message (Peer set)
// or a group message (GroupID set).
type searchHit struct {
	Peer     string    `json:"peer,omitempty"`
	GroupID  string    `json:"group_id,omitempty"`
	From     string    `json:"from,omitempty"`
	Outgoing bool      `json:"outgoing,omitempty"`
	Text     string    `json:"text"`
	TS       time.Time `json:"ts"`
}

// searchHandler answers GET /search?q=: stored direct and group messages whose
// text contains q, ignoring case, newest first. A source that cannot be read
// (persistence off, the visor RPC down) is skipped rather than failing the rest.
func searchHandler(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if q == "" {
		writeJSON(w, []searchHit{})
		return
	}
	hits := append(searchDirect(q), searchGroups(q)...)
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].TS.After(hits[j].TS) })
	if len(hits) > searchLimit {
		hits = hits[:searchLimit]
	}
	writeJSON(w, hits)
}

func searchDirect(q string) []searchHit {
	if historyStore == nil {
		return nil
	}
	peers, err := historyStore.Peers()
	if err != nil {
		return nil
	}
	var hits []searchHit
	for _, peer := range peers {
		msgs, err := historyStore.ListByPeer(peer, 0)
		if err != nil {
			continue
		}
		for i := range msgs {
			m := &msgs[i]
			// The stored body of a reply or forward is a JSON envelope; match
			// the text the user sees, not its keys.
			enrichReplyMessage(m)
			enrichForwardMessage(m)
			if !strings.Contains(strings.ToLower(m.Text), q) {
				continue
			}
			hits = append(hits, searchHit{
				Peer: m.Peer, From: m.From, Outgoing: m.Outgoing, Text: m.Text, TS: m.Timestamp,
			})
		}
	}
	return hits
}

func searchGroups(q string) []searchHit {
	var groups []visorapi.GroupInfo
	if err := pairRPCCall("GroupList", func(c visorapi.API) error {
		out, e := c.GroupList()
		groups = out
		return e
	}); err != nil {
		return nil
	}
	var hits []searchHit
	for _, g := range groups {
		if g.Status == group.StatusLeft || g.Status == group.StatusRevoked {
			continue
		}
		rows, err := groupHistoryRows(g.ID, time.Time{}, searchGroupDepth)
		if err != nil {
			continue
		}
		for _, row := range rows {
			text, _ := row["text"].(string)
			if !strings.Contains(strings.ToLower(text), q) {
				continue
			}
			from, _ := row["sender_pk"].(string)
			ts, _ := row["ts"].(time.Time)
			hits = append(hits, searchHit{GroupID: g.ID, From: from, Text: text, TS: ts})
		}
	}
	return hits
}
