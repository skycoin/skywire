package alerts

import (
	"sort"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// Target is one watched visor as seen in one check.
type Target struct {
	PK       cipher.PubKey `json:"pk"`
	Hostname string        `json:"hostname,omitempty"`
	PublicIP string        `json:"public_ip,omitempty"`
	// LastSeen is when the visor was last seen online, if known.
	LastSeen time.Time `json:"last_seen,omitzero"`
	Online   bool      `json:"online"`
	// Source is "hypervisor" or "watch".
	Source string `json:"source,omitempty"`
}

// Kind of an alert event.
const (
	KindOffline = "offline"
	KindOnline  = "online"
	KindTest    = "test"
)

// Event is one alert to deliver.
type Event struct {
	Kind   string    `json:"event"`
	Target Target    `json:"target"`
	Since  time.Time `json:"offline_since,omitzero"`
	At     time.Time `json:"at"`
}

// TargetState is a watched visor's alert state, for status reads.
type TargetState struct {
	Target
	OfflineSince time.Time `json:"offline_since,omitzero"`
	Alerted      bool      `json:"alerted,omitempty"`
}

type trackState struct {
	info      Target
	downSince time.Time
	alerted   bool
}

// Tracker turns liveness checks into offline and recovery events. One
// offline event fires once a visor has been offline for the delay, one
// recovery event when an alerted visor is back, and nothing for a visor
// that comes back within the delay.
type Tracker struct {
	states map[cipher.PubKey]*trackState
	// Floor is the earliest time an offline period counts from, so visors
	// that went down while nothing watched get the full delay to return.
	Floor time.Time
}

// NewTracker returns a Tracker. alerted lists the visors an offline alert
// was already sent for, with when they went offline, so a restart neither
// repeats the alert nor loses the recovery mail.
func NewTracker(alerted map[cipher.PubKey]time.Time) *Tracker {
	t := &Tracker{states: map[cipher.PubKey]*trackState{}}
	for pk, since := range alerted {
		t.states[pk] = &trackState{info: Target{PK: pk}, downSince: since, alerted: true}
	}
	return t
}

// Observe records one check of the whole watched set at now and returns
// the events it causes. Visors no longer in targets are dropped silently.
func (t *Tracker) Observe(now time.Time, delay time.Duration, targets []Target) []Event {
	var events []Event
	seen := make(map[cipher.PubKey]bool, len(targets))
	for _, tg := range targets {
		seen[tg.PK] = true
		st, ok := t.states[tg.PK]
		if !ok {
			st = &trackState{}
			t.states[tg.PK] = st
		}
		st.info = mergeInfo(st.info, tg)
		if tg.Online {
			if st.alerted {
				events = append(events, Event{Kind: KindOnline, Target: st.info, Since: st.downSince, At: now})
			}
			st.downSince, st.alerted = time.Time{}, false
			continue
		}
		if st.downSince.IsZero() {
			st.downSince = now
			if !tg.LastSeen.IsZero() && tg.LastSeen.Before(now) {
				st.downSince = tg.LastSeen
			}
		}
		from := st.downSince
		if from.Before(t.Floor) {
			from = t.Floor
		}
		if !st.alerted && now.Sub(from) >= delay {
			st.alerted = true
			events = append(events, Event{Kind: KindOffline, Target: st.info, Since: st.downSince, At: now})
		}
	}
	for pk := range t.states {
		if !seen[pk] {
			delete(t.states, pk)
		}
	}
	return events
}

// mergeInfo keeps known details that a check did not report.
func mergeInfo(old, cur Target) Target {
	if cur.Hostname == "" {
		cur.Hostname = old.Hostname
	}
	if cur.PublicIP == "" {
		cur.PublicIP = old.PublicIP
	}
	if cur.LastSeen.IsZero() {
		cur.LastSeen = old.LastSeen
	}
	return cur
}

// Alerted lists the visors currently alerted as offline, for persisting.
func (t *Tracker) Alerted() map[cipher.PubKey]time.Time {
	out := map[cipher.PubKey]time.Time{}
	for pk, st := range t.states {
		if st.alerted {
			out[pk] = st.downSince
		}
	}
	return out
}

// States lists every tracked visor, sorted by public key.
func (t *Tracker) States() []TargetState {
	out := make([]TargetState, 0, len(t.states))
	for _, st := range t.states {
		out = append(out, TargetState{Target: st.info, OfflineSince: st.downSince, Alerted: st.alerted})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PK.Hex() < out[j].PK.Hex() })
	return out
}
