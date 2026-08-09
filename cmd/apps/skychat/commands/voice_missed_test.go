package commands

import (
	"testing"
	"time"
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
	var out []logged
	resolveCalls(seen, incoming, active, now, func(peer, outcome string, d time.Duration) {
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
