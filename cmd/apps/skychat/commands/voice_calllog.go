// Package commands cmd/apps/skychat/commands/voice_calllog.go c4-app-chat
//
// GET /calls — the call log behind the UI's Calls tab.
//
// There is no call-log store. Every call is already written into its
// conversation as a message (voice_missed.go), so the log is those messages
// filtered back out by their handset prefix — which means it persists, prunes,
// and survives a restart exactly like the rest of the history, with no second
// schema to keep in step. The cost is one scan of the recent window per open
// of the tab, which is a bounded read of a local bolt bucket.
package commands

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// callRecord is one row of the Calls tab.
type callRecord struct {
	// Peer is the other side's public key — what a tap on the row calls back.
	Peer string `json:"peer"`
	// Outcome is one of the callMissed / callIncoming / callOutgoing labels.
	Outcome string `json:"outcome"`
	// Direction is "in" or "out", so the UI can pick an arrow without
	// re-deriving it from the label.
	Direction string `json:"direction"`
	// Seconds the call lasted; 0 for one that never connected.
	Seconds int `json:"seconds"`
	// When it ended, RFC3339 in UTC — the UI renders the date and time.
	At time.Time `json:"at"`
}

// callLogLimit bounds the history scan. Generous: a call record is a handful
// of bytes and this is a local read, but unbounded would mean the whole store.
const callLogLimit = 2000

// registerCallLogHandler wires GET /calls. Password-gated like every other
// read: the log names who this visor talks to.
func registerCallLogHandler(mux *http.ServeMux) {
	mux.HandleFunc("/calls", requireAuthFunc(callLogHandler))
}

func callLogHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	limit := 200
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= callLogLimit {
			limit = n
		}
	}
	writeJSON(w, collectCallLog(limit))
}

// collectCallLog reads recent history and keeps the call records, newest
// first. Returns an empty (non-nil) slice so the UI always gets an array.
func collectCallLog(limit int) []callRecord {
	out := []callRecord{}
	if historyStore == nil {
		return out
	}
	messages, err := historyStore.ListRecent(callLogLimit)
	if err != nil {
		chatLog.Debugf("call log: history read failed: %v", err)
		return out
	}
	// ListRecent is oldest-last; a call log reads newest-first.
	for i := len(messages) - 1; i >= 0 && len(out) < limit; i-- {
		rec, ok := parseCallRecord(messages[i].Text)
		if !ok {
			continue
		}
		rec.Peer = messages[i].Peer
		rec.At = messages[i].Timestamp
		out = append(out, rec)
	}
	return out
}

// parseCallRecord reads back what callRecordText wrote: the handset prefix,
// the outcome, and an optional `m:ss` duration.
func parseCallRecord(text string) (callRecord, bool) {
	body, ok := strings.CutPrefix(text, callTextPrefix)
	if !ok {
		return callRecord{}, false
	}
	outcome, durationText, hasDuration := strings.Cut(body, callTextSep)
	outcome = strings.TrimSpace(outcome)
	switch outcome {
	case callMissed, callIncoming, callOutgoing:
	default:
		// Someone's message that happens to start with a handset. Not ours.
		return callRecord{}, false
	}
	rec := callRecord{
		Outcome:   outcome,
		Direction: map[bool]string{true: "out", false: "in"}[outcome == callOutgoing],
	}
	if hasDuration {
		rec.Seconds = parseCallDuration(durationText)
	}
	return rec, true
}

// parseCallDuration reads `m:ss`; anything else is no duration rather than an
// error, since the text is only ever produced by callRecordText.
func parseCallDuration(text string) int {
	minutes, seconds, ok := strings.Cut(strings.TrimSpace(text), ":")
	if !ok {
		return 0
	}
	m, err := strconv.Atoi(minutes)
	if err != nil {
		return 0
	}
	s, err := strconv.Atoi(seconds)
	if err != nil {
		return 0
	}
	return m*60 + s
}
