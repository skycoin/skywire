// Package commands cmd/apps/skychat/commands/voice_missed.go c4-app-chat
//
// Every call becomes a MESSAGE in the conversation it belongs to, and the
// Calls tab is those messages read back.
//
// A call used to be the one event skychat left no trace of: it rang in the
// visor, and if the user was not looking at that moment it was gone — nothing
// in the history, nothing to scroll back to, no way to know someone tried.
// Recording it as a message is what fixes that, and it costs nothing else:
// persistence, the SSE feed and the notification path all already work on
// messages, so a call inherits every one of them with no new plumbing. It is
// also why the call log needs no store of its own — see callLogHandler.
//
// Watching rather than being told, because the visor offers no call events —
// only "who is ringing" and "who is connected" lists. A call that leaves the
// ringing list WITHOUT having appeared in the active list was not answered;
// one that passed through the active list was. Two seconds of resolution is
// ample for something whose ring lasts thirty.
package commands

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/skychat/history"
	"github.com/skycoin/skywire/pkg/visor"
)

// Every call leaves one of these in the conversation. The shared handset
// prefix is a CONTRACT, not decoration: it is what lets the Calls tab pick
// call records back out of ordinary message history, so the log survives a
// restart exactly as well as the messages around it.
const (
	callTextPrefix = "\U0001F4DE "
	callMissed     = "Missed call"
	callIncoming   = "Incoming call"
	callOutgoing   = "Outgoing call"
	// callTextSep separates the outcome from the duration in a record's text.
	callTextSep = " · "
)

// voiceWatchInterval is the poll period. Cheap: two in-process (or loopback)
// list calls returning a handful of strings.
const voiceWatchInterval = 2 * time.Second

// voiceMissedCancel stops the watcher on shutdown.
var voiceMissedCancel context.CancelFunc

// voiceWatchFailed tracks whether the last poll failed, so the reason is
// logged on the transition rather than every two seconds.
var voiceWatchFailed bool

// outgoingCalls maps a call id placed BY US to its peer.
//
// It has to be recorded at dial time because the visor cannot answer it later:
// VoiceActive returns bare call ids with no peer, and an outgoing call never
// appears in the ringing list that carries one. Without this, our own calls
// would be invisible in the log.
var outgoingCalls = struct {
	sync.Mutex
	peers map[string]string
}{peers: make(map[string]string)}

// noteOutgoingCall remembers who a call we placed is with.
func noteOutgoingCall(callID, peer string) {
	if callID == "" || peer == "" {
		return
	}
	outgoingCalls.Lock()
	outgoingCalls.peers[callID] = peer
	outgoingCalls.Unlock()
}

func takeOutgoingCall(callID string) (string, bool) {
	outgoingCalls.Lock()
	defer outgoingCalls.Unlock()
	peer, ok := outgoingCalls.peers[callID]
	delete(outgoingCalls.peers, callID)
	return peer, ok
}

// startVoiceMissedWatcher records calls as messages. No-op without the
// pair-RPC channel, which is the only way to reach the visor's call lists.
func startVoiceMissedWatcher(parent context.Context) {
	if !pairEnable {
		return
	}
	ctx, cancel := context.WithCancel(parent) //nolint:gosec
	voiceMissedCancel = cancel
	appLog("Voice: watching calls for the call log")
	go watchMissedCalls(ctx)
}

// stopVoiceMissedWatcher ends the loop on shutdown.
func stopVoiceMissedWatcher() {
	if voiceMissedCancel != nil {
		voiceMissedCancel()
		voiceMissedCancel = nil
	}
}

// callWatch is what the loop remembers between polls about one call.
type callWatch struct {
	peer        string
	outgoing    bool
	connectedAt time.Time
}

// watchMissedCalls is the loop, split out so a test can drive it directly.
func watchMissedCalls(ctx context.Context) {
	seen := make(map[string]*callWatch)

	ticker := time.NewTicker(voiceWatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		var incoming, active []string
		err := pairRPCCall("VoiceIncoming", func(c visor.API) error {
			out, e := c.VoiceIncoming()
			incoming = out
			return e
		})
		if err != nil {
			// Logged on the transition, not per tick: with voice off this
			// would otherwise be a line every two seconds forever.
			if !voiceWatchFailed {
				voiceWatchFailed = true
				appLog("Voice: cannot read the call list (%v) — calls will not be logged", err)
			}
			continue
		}
		if voiceWatchFailed {
			voiceWatchFailed = false
			appLog("Voice: call list readable again")
		}
		if err := pairRPCCall("VoiceActive", func(c visor.API) error {
			out, e := c.VoiceActive()
			active = out
			return e
		}); err != nil {
			// Half a picture is worse than none: without the active list every
			// call that stopped ringing looks missed, including the answered
			// one currently in progress.
			continue
		}
		resolveCalls(seen, incoming, active, time.Now(), recordCall)
	}
}

// resolveCalls advances the watcher's state by one poll and records every call
// that ended. The recorder is a parameter so the state machine — the part that
// decides what each outcome means — can be pinned by a test with no visor,
// history store or hub.
func resolveCalls(
	seen map[string]*callWatch,
	incoming, active []string,
	now time.Time,
	record func(peer, outcome string, duration time.Duration),
) {
	ringing := make(map[string]bool, len(incoming))
	for _, line := range incoming {
		id, peer, ok := parseVoiceInvite(line)
		if !ok {
			continue
		}
		ringing[id] = true
		if seen[id] == nil {
			seen[id] = &callWatch{peer: peer}
		}
	}

	live := make(map[string]bool, len(active))
	for _, id := range active {
		live[id] = true
		call := seen[id]
		if call == nil {
			// Never rang here, so it is one we placed. The peer comes from the
			// dial, which is the only place it was ever known.
			peer, ok := takeOutgoingCall(id)
			if !ok {
				continue
			}
			call = &callWatch{peer: peer, outgoing: true}
			seen[id] = call
		}
		if call.connectedAt.IsZero() {
			call.connectedAt = now
		}
	}

	for id, call := range seen {
		if ringing[id] || live[id] {
			continue
		}
		switch {
		case call.outgoing:
			record(call.peer, callOutgoing, now.Sub(call.connectedAt))
		case !call.connectedAt.IsZero():
			record(call.peer, callIncoming, now.Sub(call.connectedAt))
		default:
			// Declined and never-picked-up are the same two lists from here:
			// both are a call that rang and stopped. Telling them apart needs
			// a decline signal shared by BOTH answer paths (the chat page's
			// /voice/decline and the phone's visor API), which does not exist
			// yet — so they read as one thing rather than as a guess.
			record(call.peer, callMissed, 0)
		}
		delete(seen, id)
	}
}

// parseVoiceInvite splits the visor's `"<call-id> from <peer-pk>"` listing.
func parseVoiceInvite(line string) (id, peer string, ok bool) {
	const sep = " from "
	at := strings.Index(line, sep)
	if at <= 0 {
		return "", "", false
	}
	id = strings.TrimSpace(line[:at])
	peer = strings.TrimSpace(line[at+len(sep):])
	return id, peer, id != "" && peer != ""
}

// callRecordText renders one record: the outcome, and for a call that actually
// connected, how long it lasted. A missed call has no duration to show and
// gets none — "0:00" would read as a call that connected and said nothing.
func callRecordText(outcome string, duration time.Duration) string {
	if duration < time.Second {
		return callTextPrefix + outcome
	}
	total := int(duration.Seconds())
	return fmt.Sprintf("%s%s%s%d:%02d", callTextPrefix, outcome, callTextSep, total/60, total%60)
}

// recordCall writes the record into the conversation with peer: stored in
// history, pushed to every attached UI, and — for the one outcome that is
// news — announced the way an inbound message is.
func recordCall(peer, outcome string, duration time.Duration) {
	id := newEventID()
	text := callRecordText(outcome, duration)

	persistMessage(history.Message{
		Peer:      peer,
		From:      peer,
		Outgoing:  outcome == callOutgoing,
		Text:      text,
		Timestamp: time.Now().UTC(),
		ID:        id,
	})
	if hub != nil {
		hub.publishEvent(chatEvent{
			ID:        id,
			Channel:   channelDM,
			Transport: "voice",
			Dir:       map[bool]string{true: "out", false: "in"}[outcome == callOutgoing],
			From:      peer,
			Text:      text,
			Len:       len(text),
		})
	}
	// Only a missed call is worth interrupting for. The other two are records
	// of something the user was already part of.
	if outcome == callMissed {
		notifyOSInbound(shortHexPK(peer), text)
	}
	appLog("Voice: %s with %s logged", strings.ToLower(outcome), shortHexPK(peer))
}
