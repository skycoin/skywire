package commands

import (
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// logged is one call the watcher decided to record.
type logged struct {
	peer     string
	outcome  string
	duration time.Duration
}

// poll drives one round of the watcher and returns what it recorded.
func poll(seen map[string]*callWatch, incoming, active []string) []logged {
	return pollAt(seen, incoming, active, time.Now())
}

func pollAt(seen map[string]*callWatch, incoming, active []string, now time.Time) []logged {
	return pollDialAt(seen, incoming, active, nil, now)
}

func pollDialAt(seen map[string]*callWatch, incoming, active []string, dialing []visorapi.VoiceDialingInfo, now time.Time) []logged {
	var out []logged
	resolveCalls(seen, incoming, active, dialing, now, func(peer, outcome string, d time.Duration) {
		out = append(out, logged{peer, outcome, d})
	})
	return out
}

const (
	alice = "0311111111111111111111111111111111111111111111111111111111111111aa"
	bob   = "0322222222222222222222222222222222222222222222222222222222222222bb"
)

// A call that rang and stopped without ever connecting.
func TestUnansweredCallIsLoggedAsMissed(t *testing.T) {
	seen := map[string]*callWatch{}

	if got := poll(seen, []string{"c1 from " + alice}, nil); len(got) != 0 {
		t.Fatalf("recorded %v while the call was still ringing", got)
	}
	got := poll(seen, nil, nil)
	if len(got) != 1 || got[0].peer != alice || got[0].outcome != callMissed {
		t.Fatalf("recorded %v, want one %s from %s", got, callMissed, alice)
	}
	if got[0].duration != 0 {
		t.Fatalf("missed call has duration %v, want none", got[0].duration)
	}
	// And exactly once — a repeat would notify again on every poll for the
	// rest of the session.
	if again := poll(seen, nil, nil); len(again) != 0 {
		t.Fatalf("recorded %v a second time", again)
	}
}

// Answered: ringing → active → gone. Logged as an incoming call, and its
// duration is measured from the poll that first saw it connected.
func TestAnsweredCallIsLoggedWithItsDuration(t *testing.T) {
	seen := map[string]*callWatch{}
	start := time.Now()

	pollAt(seen, []string{"c1 from " + alice}, nil, start)
	pollAt(seen, nil, []string{"c1"}, start) // answered, no longer ringing
	got := pollAt(seen, nil, nil, start.Add(90*time.Second))

	if len(got) != 1 || got[0].outcome != callIncoming {
		t.Fatalf("recorded %v, want one %s", got, callIncoming)
	}
	if got[0].duration != 90*time.Second {
		t.Fatalf("duration = %v, want 90s", got[0].duration)
	}
}

// A call can appear in both lists in the same poll — the ring is still listed
// when the answer lands. That counts as answered, not missed.
func TestRingingAndActiveInTheSamePollIsAnswered(t *testing.T) {
	seen := map[string]*callWatch{}

	poll(seen, []string{"c1 from " + alice}, nil)
	poll(seen, []string{"c1 from " + alice}, []string{"c1"})
	got := poll(seen, nil, nil)
	if len(got) != 1 || got[0].outcome != callIncoming {
		t.Fatalf("recorded %v, want one %s", got, callIncoming)
	}
}

// A call we placed never rings here, so its peer can only come from the dial.
func TestOutgoingCallIsLoggedFromTheDialRecord(t *testing.T) {
	seen := map[string]*callWatch{}
	noteOutgoingCall("c9", bob)

	poll(seen, nil, []string{"c9"})
	got := poll(seen, nil, nil)
	if len(got) != 1 || got[0].peer != bob || got[0].outcome != callOutgoing {
		t.Fatalf("recorded %v, want one %s to %s", got, callOutgoing, bob)
	}
}

// An active call with no dial record and no ring is not ours to attribute —
// logging it against an empty peer would put a row in nobody's conversation.
func TestActiveCallWithNoPeerIsIgnored(t *testing.T) {
	seen := map[string]*callWatch{}

	poll(seen, nil, []string{"unknown"})
	if got := poll(seen, nil, nil); len(got) != 0 {
		t.Fatalf("recorded %v for a call with no known peer", got)
	}
}

func TestCallsAreTrackedIndependently(t *testing.T) {
	seen := map[string]*callWatch{}

	poll(seen, []string{"c1 from " + alice, "c2 from " + bob}, nil)
	// One poll later bob has answered and alice has given up: both leave the
	// ringing list together, with different outcomes.
	got := poll(seen, nil, []string{"c2"})
	if len(got) != 1 || got[0].peer != alice || got[0].outcome != callMissed {
		t.Fatalf("recorded %v, want alice's missed call only", got)
	}
	ended := poll(seen, nil, nil)
	if len(ended) != 1 || ended[0].peer != bob || ended[0].outcome != callIncoming {
		t.Fatalf("recorded %v when bob's call ended, want one %s", ended, callIncoming)
	}
}

func TestParseVoiceInvite(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		id, peer   string
		wantParsed bool
	}{
		{"the visor's shape", "abc123 from " + alice, "abc123", alice, true},
		{"no separator", "abc123", "", "", false},
		{"empty id", " from " + alice, "", "", false},
		{"empty peer", "abc123 from ", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, peer, ok := parseVoiceInvite(tt.line)
			if ok != tt.wantParsed {
				t.Fatalf("parsed = %v, want %v", ok, tt.wantParsed)
			}
			if ok && (id != tt.id || peer != tt.peer) {
				t.Fatalf("got (%q, %q), want (%q, %q)", id, peer, tt.id, tt.peer)
			}
		})
	}
}

// The call log is history read back, so what recordCall writes and what the
// /calls handler parses have to agree exactly — a round trip, not two guesses.
func TestCallRecordTextRoundTrips(t *testing.T) {
	tests := []struct {
		outcome   string
		duration  time.Duration
		wantSecs  int
		wantDirIn bool
	}{
		{callMissed, 0, 0, true},
		{callIncoming, 90 * time.Second, 90, true},
		{callOutgoing, 3671 * time.Second, 3671, false},
		// Under a second reads as "did not connect" rather than "0:00".
		{callIncoming, 400 * time.Millisecond, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.outcome, func(t *testing.T) {
			text := callRecordText(tt.outcome, tt.duration)
			rec, ok := parseCallRecord(text)
			if !ok {
				t.Fatalf("parseCallRecord(%q) did not recognize its own output", text)
			}
			if rec.Outcome != tt.outcome {
				t.Fatalf("outcome = %q, want %q", rec.Outcome, tt.outcome)
			}
			if rec.Seconds != tt.wantSecs {
				t.Fatalf("seconds = %d, want %d (from %q)", rec.Seconds, tt.wantSecs, text)
			}
			wantDir := map[bool]string{true: "in", false: "out"}[tt.wantDirIn]
			if rec.Direction != wantDir {
				t.Fatalf("direction = %q, want %q", rec.Direction, wantDir)
			}
		})
	}
}

// An ordinary message that happens to start with a handset emoji is not a
// call record, and must not appear in the Calls tab.
func TestParseCallRecordRejectsOrdinaryMessages(t *testing.T) {
	for _, text := range []string{
		"hello",
		callTextPrefix + "call me back",
		"Missed call",
		"",
	} {
		if _, ok := parseCallRecord(text); ok {
			t.Fatalf("parseCallRecord(%q) accepted a message that is not a call record", text)
		}
	}
}

// dial is the visor's dialing list holding one call, c1 to bob.
func dial(state string) []visorapi.VoiceDialingInfo {
	return []visorapi.VoiceDialingInfo{{CallID: "c1", Peer: bob, State: state}}
}

// A call we placed that rang out is logged once as unanswered, even though
// the visor keeps it listed with its outcome for a few seconds after.
func TestUnansweredOutgoingCallIsLogged(t *testing.T) {
	seen := map[string]*callWatch{}
	now := time.Now()

	if got := pollDialAt(seen, nil, nil, dial("ringing"), now); len(got) != 0 {
		t.Fatalf("recorded %v while the call was still ringing", got)
	}
	got := pollDialAt(seen, nil, nil, dial("no_answer"), now)
	if len(got) != 1 || got[0].peer != bob || got[0].outcome != callUnanswered || got[0].duration != 0 {
		t.Fatalf("recorded %v, want one %s to %s", got, callUnanswered, bob)
	}
	if again := pollDialAt(seen, nil, nil, dial("no_answer"), now); len(again) != 0 {
		t.Fatalf("recorded %v again while the outcome lingered", again)
	}
	pollDialAt(seen, nil, nil, nil, now)
	if len(seen) != 0 {
		t.Fatalf("watcher still holds %d calls after the outcome expired", len(seen))
	}
}

// Hanging up during the ring drops the call from the list with no outcome.
func TestCancelledOutgoingCallIsLoggedUnanswered(t *testing.T) {
	seen := map[string]*callWatch{}
	pollDialAt(seen, nil, nil, dial("connecting"), time.Now())
	got := pollDialAt(seen, nil, nil, nil, time.Now())
	if len(got) != 1 || got[0].outcome != callUnanswered {
		t.Fatalf("recorded %v, want one %s", got, callUnanswered)
	}
}

// Answered: dialing, then active, then gone is an outgoing call with a length.
func TestAnsweredOutgoingCallFromTheDialingList(t *testing.T) {
	seen := map[string]*callWatch{}
	start := time.Now()
	pollDialAt(seen, nil, nil, dial("ringing"), start)
	pollDialAt(seen, nil, []string{"c1"}, nil, start)
	got := pollDialAt(seen, nil, nil, nil, start.Add(time.Minute))
	if len(got) != 1 || got[0].outcome != callOutgoing || got[0].duration != time.Minute {
		t.Fatalf("recorded %v, want one %s of 1m", got, callOutgoing)
	}
}

// A dialing list that could not be read must not end a ringing call.
func TestUnreadableDialingListKeepsTheCallRinging(t *testing.T) {
	seen := map[string]*callWatch{}
	pollDialAt(seen, nil, nil, dial("ringing"), time.Now())
	if got := pollDialAt(seen, nil, nil, stillDialing(seen), time.Now()); len(got) != 0 {
		t.Fatalf("recorded %v on a failed poll", got)
	}
}

func TestUnansweredRecordRoundTrips(t *testing.T) {
	rec, ok := parseCallRecord(callRecordText(callUnanswered, 0))
	if !ok || rec.Outcome != callUnanswered || rec.Direction != "out" {
		t.Fatalf("parsed %+v (ok=%v), want an outgoing %s", rec, ok, callUnanswered)
	}
}

// A call that never reached its peer is parked for the peer as a missed call.
// One that rang out was logged there already.
func TestOfflineCallIsParkedForThePeer(t *testing.T) {
	var parked []string
	orig := parkMissedCall
	parkMissedCall = func(peer, callID string) { parked = append(parked, peer+"/"+callID) }
	t.Cleanup(func() { parkMissedCall = orig })

	seen := map[string]*callWatch{}
	pollDialAt(seen, nil, nil, dial("connecting"), time.Now())
	pollDialAt(seen, nil, nil, dial("offline"), time.Now())
	pollDialAt(seen, nil, nil, nil, time.Now())
	if len(parked) != 1 || parked[0] != bob+"/c1" {
		t.Fatalf("parked %v, want the call for %s", parked, bob)
	}

	parked = nil
	seen = map[string]*callWatch{}
	pollDialAt(seen, nil, nil, dial("ringing"), time.Now())
	pollDialAt(seen, nil, nil, dial("no_answer"), time.Now())
	pollDialAt(seen, nil, nil, nil, time.Now())
	if len(parked) != 0 {
		t.Fatalf("parked %v for a call that rang", parked)
	}
}
