// Package commands cmd/apps/skychat/commands/delete_outbox.go c4-app-chat
//
// Deletes for everyone that the peer has not confirmed yet.
//
// A chat-delete used to be sent once. A peer that was offline, or a cached
// conn that had died without saying so, lost it, and the peer kept the message
// although the deleter's copy was gone. Each delete now waits here, on disk,
// and is sent again with backoff until the peer acknowledges it
// (dm.DeleteAckID) or it is deleteGiveUp old. Sending one twice is harmless,
// because the receiver forgets a message it no longer holds without complaint,
// and a peer too old to acknowledge only receives it a few more times.
package commands

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"
)

const (
	deleteRetryFirst = time.Minute
	deleteRetryMax   = 30 * time.Minute
	deleteGiveUp     = 14 * 24 * time.Hour
	deleteScanEvery  = 20 * time.Second
	deleteSendBudget = 30 * time.Second
)

// deleteOutbox holds, per peer, the ids of deletes it has not acknowledged and
// when each was first asked. The backoff is per peer and kept in memory only.
type deleteOutbox struct {
	mu    sync.Mutex
	path  string
	asked map[string]map[string]time.Time
	next  map[string]time.Time
	wait  map[string]time.Duration
}

// deletes is nil until openDeleteOutbox, and every method tolerates that.
var deletes *deleteOutbox

func deleteOutboxPath() string {
	return identityStatePath(stateDir(), identityScope(), "skychat-deletes.json")
}

func openDeleteOutbox(path string) {
	o := &deleteOutbox{path: path, asked: map[string]map[string]time.Time{}, next: map[string]time.Time{}, wait: map[string]time.Duration{}}
	if blob, err := os.ReadFile(path); err == nil { //nolint:gosec // path is our own state file
		if err := json.Unmarshal(blob, &o.asked); err != nil {
			appLog("skychat: delete outbox %s unreadable, starting empty: %v", path, err)
			o.asked = map[string]map[string]time.Time{}
		}
	}
	deletes = o
}

// add records a delete to peer, first sent just now, so the next try waits.
func (o *deleteOutbox) add(peer, id string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.asked[peer] == nil {
		o.asked[peer] = map[string]time.Time{}
	}
	if _, ok := o.asked[peer][id]; !ok {
		o.asked[peer][id] = time.Now().UTC()
	}
	if _, ok := o.next[peer]; !ok {
		o.next[peer] = time.Now().Add(deleteRetryFirst)
		o.wait[peer] = deleteRetryFirst
	}
	o.saveLocked()
}

// acked drops a delete the peer confirmed. The peer is reachable, so what it
// still owes is tried again soon rather than after a long backoff.
func (o *deleteOutbox) acked(peer, id string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.asked[peer][id]; !ok {
		return
	}
	delete(o.asked[peer], id)
	if len(o.asked[peer]) == 0 {
		delete(o.asked, peer)
		delete(o.next, peer)
		delete(o.wait, peer)
	} else {
		o.next[peer] = time.Now().Add(deleteRetryFirst)
		o.wait[peer] = deleteRetryFirst
	}
	o.saveLocked()
}

// due returns the deletes to send again now, per peer, and pushes each such
// peer's next try back by its doubled wait. Deletes past deleteGiveUp go.
func (o *deleteOutbox) due(now time.Time) map[string][]string {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	out := map[string][]string{}
	dropped := false
	for peer, ids := range o.asked {
		for id, at := range ids {
			if now.Sub(at) > deleteGiveUp {
				delete(ids, id)
				dropped = true
			}
		}
		if len(ids) == 0 {
			delete(o.asked, peer)
			delete(o.next, peer)
			delete(o.wait, peer)
			continue
		}
		if next, ok := o.next[peer]; ok && now.Before(next) {
			continue
		}
		for id := range ids {
			out[peer] = append(out[peer], id)
		}
		w := 2 * o.wait[peer]
		if w < deleteRetryFirst {
			w = deleteRetryFirst
		}
		if w > deleteRetryMax {
			w = deleteRetryMax
		}
		o.wait[peer] = w
		o.next[peer] = now.Add(w)
	}
	if dropped {
		o.saveLocked()
	}
	return out
}

func (o *deleteOutbox) saveLocked() {
	blob, err := json.Marshal(o.asked)
	if err != nil {
		return
	}
	tmp := o.path + ".tmp"
	if err := os.WriteFile(tmp, blob, 0o600); err != nil {
		appLog("skychat: delete outbox save: %v", err)
		return
	}
	if err := os.Rename(tmp, o.path); err != nil {
		appLog("skychat: delete outbox save: %v", err)
	}
}

// startDeleteRetries sends due deletes again until ctx ends. One failed send
// stops that peer's round, because the peer is unreachable for the rest too.
func startDeleteRetries(ctx context.Context) {
	if deletes == nil {
		return
	}
	go func() {
		t := time.NewTicker(deleteScanEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			if chatCtrl == nil {
				continue
			}
			for peer, ids := range deletes.due(time.Now()) {
				pk, err := parsePK(peer)
				if err != nil {
					continue
				}
				for _, id := range ids {
					sctx, cancel := context.WithTimeout(ctx, deleteSendBudget)
					err := chatCtrl.SendDelete(sctx, pk, id)
					cancel()
					if err != nil {
						break
					}
				}
			}
		}
	}()
}
