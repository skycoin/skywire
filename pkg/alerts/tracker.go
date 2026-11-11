package alerts

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// Target is one watched visor as seen in one check.
type Target struct {
	PK       cipher.PubKey `json:"pk"`
	Hostname string        `json:"hostname,omitempty"`
	PublicIP string        `json:"public_ip,omitempty"`
	LocalIP  string        `json:"local_ip,omitempty"`
	// LastSeen is when the visor was last seen online, if known.
	LastSeen time.Time `json:"last_seen,omitzero"`
	Online   bool      `json:"online"`
	// State is one of the State values. Empty means StateOnline or
	// StateOffline, from Online.
	State string `json:"state,omitempty"`
	// Source is "hypervisor" or "watch".
	Source string `json:"source,omitempty"`
	// Uptime is how long the board has been up at the check, the host's
	// uptime when the visor reports it and the visor's own otherwise.
	Uptime Duration `json:"uptime,omitempty"`
	// Storage is the board's last reported storage health.
	Storage *Storage `json:"storage,omitempty"`
}

// Storage is a board's storage health, the early signs of a failing SD
// card.
type Storage struct {
	ReadOnlyRoot    bool    `json:"root_read_only,omitempty"`
	IOErrors        int     `json:"io_errors,omitempty"`
	LastIOError     string  `json:"last_io_error,omitempty"`
	DiskUsedPercent float64 `json:"disk_used_percent,omitempty"`
}

// Kind of an alert event.
const (
	KindOffline  = "offline"
	KindOnline   = "online"
	KindStorage  = "storage"
	KindConflict = "ip-conflict"
	KindTest     = "test"
	// KindLinkLost is a visor that is up but has not reconnected to its
	// hypervisor for the offline delay.
	KindLinkLost = "link-lost"
)

// States of a watched visor. A hypervisor reports all five for the visors
// it manages; a watch list visor is only online or offline.
const (
	StateOnline = "online"
	// StateReconnecting is a visor whose link to the hypervisor dropped
	// but which still answers the hypervisor's call back.
	StateReconnecting = "reconnecting"
	// StateRestarting is a visor that said it is going down to restart.
	StateRestarting = "restarting"
	// StateDetached is a visor that removed this hypervisor. It is not
	// watched.
	StateDetached = "detached"
	StateOffline  = "offline"
)

// DiagLinkRestored is the diagnosis of the recovery after KindLinkLost.
const DiagLinkRestored = "the visor reconnected to its hypervisor"

// ConflictWindow is how recently a visor must have been online for its
// LAN IP to count in a conflict, so a dead board's stale address does not.
const ConflictWindow = 10 * time.Minute

// Scope of an outage: one board, or every watched visor behind one
// public IP.
const (
	ScopeBoard = "board"
	ScopeSite  = "site"
)

// SiteWindow is how close together every watched visor behind one public
// IP must go offline to count as one site outage.
const SiteWindow = 2 * time.Minute

// Recovery reasons, from the board's uptime against the outage length.
const (
	ReasonRebooted = "rebooted during the outage (likely power loss or crash)"
	ReasonStayedUp = "stayed up (network or internet outage)"
)

// Event is one alert to deliver.
type Event struct {
	Kind  string `json:"event"`
	Scope string `json:"kind,omitempty"`
	// Target is the board, or for a site only its public IP.
	Target Target `json:"target"`
	// Site is the public IP of a site event and Members its visors.
	Site           string    `json:"site,omitempty"`
	Members        []Target  `json:"members,omitempty"`
	Since          time.Time `json:"offline_since,omitzero"`
	At             time.Time `json:"at"`
	Diagnosis      string    `json:"diagnosis,omitempty"`
	RecoveryReason string    `json:"recovery_reason,omitempty"`
	Warnings       []string  `json:"warnings,omitempty"`
}

// TargetState is a watched visor's alert state, for status reads.
type TargetState struct {
	Target
	OfflineSince time.Time `json:"offline_since,omitzero"`
	Alerted      bool      `json:"alerted,omitempty"`
	// Site is set while the visor is part of an alerted site outage.
	Site            string   `json:"site,omitempty"`
	StorageWarnings []string `json:"storage_warnings,omitempty"`
	// LinkLostSince is when a reconnecting visor lost its hypervisor link.
	LinkLostSince time.Time `json:"link_lost_since,omitzero"`
	LinkAlerted   bool      `json:"link_alerted,omitempty"`
}

// Saved is one visor's alert state kept across restarts.
type Saved struct {
	Since   time.Time `json:"since,omitzero"`
	Site    string    `json:"site,omitempty"`
	Storage []string  `json:"storage,omitempty"`
	// Lost is when the link lost alert's outage began.
	Lost time.Time `json:"lost,omitzero"`
}

type trackState struct {
	info      Target
	downSince time.Time
	alerted   bool
	site      string
	// storage maps each storage warning already sent to its text.
	storage map[string]string
	// lostSince and lostAlerted track a reconnecting visor.
	lostSince   time.Time
	lostAlerted bool
	// restarted is set while the visor is restarting, so its offline
	// delay counts from the end of the restart grace.
	restarted bool
}

// held reports a visor that is neither online nor offline: its offline
// delay does not run.
func held(state string) bool {
	return state == StateReconnecting || state == StateRestarting
}

// normalize fills State from Online and makes Online agree with State.
func normalize(tg Target) Target {
	if tg.State == "" {
		tg.State = StateOffline
		if tg.Online {
			tg.State = StateOnline
		}
	}
	tg.Online = tg.State == StateOnline
	return tg
}

type siteState struct {
	since  time.Time
	backAt time.Time
}

// Tracker turns liveness checks into offline and recovery events. One
// offline event fires once a visor has been offline for the delay, one
// recovery event when an alerted visor is back, and nothing for a visor
// that comes back within the delay. When every watched visor behind one
// public IP goes down together, one site event replaces the board events.
type Tracker struct {
	states map[cipher.PubKey]*trackState
	sites  map[string]*siteState
	// conflicts maps "public IP|LAN IP" to the keys already alerted.
	conflicts map[string]string
	// Floor is the earliest time an offline period counts from, so visors
	// that went down while nothing watched get the full delay to return.
	Floor time.Time
	// DiskWarn is the disk use percent that raises a storage warning.
	// Zero is DefaultDiskWarn.
	DiskWarn float64
}

// NewTracker returns a Tracker restored from saved, so a restart neither
// repeats an alert nor loses the recovery mail.
func NewTracker(saved map[cipher.PubKey]Saved) *Tracker {
	t := &Tracker{states: map[cipher.PubKey]*trackState{}, sites: map[string]*siteState{}, conflicts: map[string]string{}}
	for pk, s := range saved {
		st := &trackState{info: Target{PK: pk}, storage: map[string]string{}}
		if !s.Lost.IsZero() {
			st.lostSince, st.lostAlerted = s.Lost, true
		}
		for _, k := range s.Storage {
			st.storage[k] = ""
		}
		if !s.Since.IsZero() {
			st.downSince, st.alerted, st.site = s.Since, true, s.Site
			if s.Site != "" {
				if ss := t.sites[s.Site]; ss == nil || s.Since.Before(ss.since) {
					t.sites[s.Site] = &siteState{since: s.Since}
				}
			}
		}
		t.states[pk] = st
	}
	return t
}

// Observe records one check of the whole watched set at now and returns
// the events it causes. Visors no longer in targets, or detached, are
// dropped silently. Reconnecting and restarting visors are held: their
// offline delay does not run.
func (t *Tracker) Observe(now time.Time, delay time.Duration, targets []Target) []Event {
	var events []Event
	seen := make(map[cipher.PubKey]bool, len(targets))
	for _, tg := range targets {
		tg = normalize(tg)
		if tg.State == StateDetached {
			continue
		}
		seen[tg.PK] = true
		st, ok := t.states[tg.PK]
		if !ok {
			st = &trackState{storage: map[string]string{}}
			t.states[tg.PK] = st
		}
		st.info = mergeInfo(st.info, tg)
		switch {
		case held(tg.State):
			if !st.alerted {
				st.downSince = time.Time{}
			}
			st.restarted = st.restarted || tg.State == StateRestarting
		case tg.State == StateOffline && st.downSince.IsZero():
			st.downSince = now
			if !st.restarted && !tg.LastSeen.IsZero() && tg.LastSeen.Before(now) {
				st.downSince = tg.LastSeen
			}
			st.restarted = false
		case tg.State == StateOnline:
			st.restarted = false
		}
	}
	for pk := range t.states {
		if !seen[pk] {
			delete(t.states, pk)
		}
	}
	order := t.sorted()

	for _, st := range order {
		if !st.info.Online {
			continue
		}
		if st.alerted && st.site == "" {
			events = append(events, t.boardRecovery(st, now))
		}
		if st.site == "" {
			st.downSince, st.alerted = time.Time{}, false
		}
	}
	events = append(events, t.siteRecoveries(now, delay, order)...)

	bySite := map[string][]*trackState{}
	for _, st := range order {
		if ip := st.info.PublicIP; ip != "" {
			bySite[ip] = append(bySite[ip], st)
		}
	}
	events = append(events, t.linkEvents(now, delay, order)...)

	for _, st := range order {
		if st.info.Online || st.alerted || held(st.info.State) {
			continue
		}
		from := st.downSince
		if from.Before(t.Floor) {
			from = t.Floor
		}
		if now.Sub(from) < delay {
			continue
		}
		if ev, ok := t.siteOutage(st, bySite[st.info.PublicIP], now); ok {
			events = append(events, ev)
			continue
		}
		st.alerted = true
		events = append(events, Event{Kind: KindOffline, Scope: ScopeBoard, Target: st.info,
			Since: st.downSince, At: now, Diagnosis: storageNote(st.info.Storage, t.diskWarn(), "had storage errors before going offline")})
	}

	for _, st := range order {
		if !st.info.Online || st.info.Storage == nil {
			continue
		}
		next := map[string]string{}
		var fresh []string
		for _, w := range storageWarnings(st.info.Storage, t.diskWarn(), st.storage) {
			next[w.key] = w.text
			if _, ok := st.storage[w.key]; !ok {
				fresh = append(fresh, w.text)
			}
		}
		st.storage = next
		if len(fresh) > 0 {
			events = append(events, Event{Kind: KindStorage, Scope: ScopeBoard, Target: st.info, At: now, Warnings: fresh})
		}
	}
	return append(events, t.ipConflicts(now, order)...)
}

// linkEvents alerts once when a visor stays reconnecting for the delay,
// and sends the recovery when it is online again.
func (t *Tracker) linkEvents(now time.Time, delay time.Duration, order []*trackState) []Event {
	var events []Event
	for _, st := range order {
		switch st.info.State {
		case StateReconnecting:
			if st.lostSince.IsZero() {
				st.lostSince = now
				if ls := st.info.LastSeen; !ls.IsZero() && ls.Before(now) {
					st.lostSince = ls
				}
			}
			from := st.lostSince
			if from.Before(t.Floor) {
				from = t.Floor
			}
			if !st.lostAlerted && !st.alerted && now.Sub(from) >= delay {
				st.lostAlerted = true
				events = append(events, Event{Kind: KindLinkLost, Scope: ScopeBoard, Target: st.info, Since: st.lostSince, At: now,
					Diagnosis: "the visor answers the hypervisor's call back but has not reconnected"})
			}
		case StateOnline:
			if st.lostAlerted {
				events = append(events, Event{Kind: KindOnline, Scope: ScopeBoard, Target: st.info, Since: st.lostSince, At: now,
					Diagnosis: DiagLinkRestored})
			}
			st.lostSince, st.lostAlerted = time.Time{}, false
		case StateOffline:
			st.lostSince, st.lostAlerted = time.Time{}, false
		}
	}
	return events
}

// ipConflicts alerts once when two or more recently online visors behind
// one public IP report the same LAN IP, and again only if the set changes.
func (t *Tracker) ipConflicts(now time.Time, order []*trackState) []Event {
	groups := map[string][]*trackState{}
	var keys []string
	for _, st := range order {
		i := st.info
		if i.PublicIP == "" || i.LocalIP == "" || (!i.Online && now.Sub(i.LastSeen) > ConflictWindow) {
			continue
		}
		k := i.PublicIP + "|" + i.LocalIP
		if groups[k] == nil {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], st)
	}
	var events []Event
	live := map[string]bool{}
	for _, k := range keys {
		g := groups[k]
		if len(g) < 2 {
			continue
		}
		live[k] = true
		pks := make([]string, len(g))
		for i, st := range g {
			pks[i] = st.info.PK.String()
		}
		set := strings.Join(pks, ",")
		if t.conflicts[k] == set {
			continue
		}
		t.conflicts[k] = set
		ip := g[0].info.PublicIP
		ev := Event{Kind: KindConflict, Scope: ScopeSite, Site: ip, At: now,
			Target:    Target{PublicIP: ip, LocalIP: g[0].info.LocalIP},
			Diagnosis: fmt.Sprintf("%d visors report LAN IP %s: %s", len(g), g[0].info.LocalIP, strings.Join(pks, ", "))}
		for _, st := range g {
			ev.Members = append(ev.Members, st.info)
		}
		events = append(events, ev)
	}
	for k := range t.conflicts {
		if !live[k] {
			delete(t.conflicts, k)
		}
	}
	return events
}

func (t *Tracker) sorted() []*trackState {
	out := make([]*trackState, 0, len(t.states))
	for _, st := range t.states {
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].info.PK.Hex() < out[j].info.PK.Hex() })
	return out
}

func (t *Tracker) diskWarn() float64 {
	if t.DiskWarn <= 0 {
		return DefaultDiskWarn
	}
	return t.DiskWarn
}

func (t *Tracker) boardRecovery(st *trackState, now time.Time) Event {
	return Event{Kind: KindOnline, Scope: ScopeBoard, Target: st.info, Since: st.downSince, At: now,
		RecoveryReason: RecoveryReason(time.Duration(st.info.Uptime), now.Sub(st.downSince)),
		Diagnosis:      storageNote(st.info.Storage, t.diskWarn(), "reports storage problems")}
}

// siteOutage reports one site event when every watched visor behind st's
// public IP is offline and those not already alerted went down together.
func (t *Tracker) siteOutage(st *trackState, members []*trackState, now time.Time) (Event, bool) {
	ip := st.info.PublicIP
	if ip == "" || len(members) < 2 || t.sites[ip] != nil {
		return Event{}, false
	}
	var fresh []*trackState
	lo, hi := st.downSince, st.downSince
	for _, m := range members {
		if m.info.Online || held(m.info.State) {
			return Event{}, false
		}
		if m.alerted {
			continue
		}
		fresh = append(fresh, m)
		if m.downSince.Before(lo) {
			lo = m.downSince
		}
		if m.downSince.After(hi) {
			hi = m.downSince
		}
	}
	if len(fresh) < 2 || hi.Sub(lo) > SiteWindow {
		return Event{}, false
	}
	t.sites[ip] = &siteState{since: lo}
	ev := Event{Kind: KindOffline, Scope: ScopeSite, Site: ip, Target: Target{PublicIP: ip}, Since: lo, At: now}
	notes := []string{fmt.Sprintf("all %d watched visors behind this public IP went offline together", len(members))}
	for _, m := range members {
		ev.Members = append(ev.Members, m.info)
		if n := storageNote(m.info.Storage, t.diskWarn(), "had storage errors before going offline"); n != "" {
			notes = append(notes, displayName(m.info)+" "+n)
		}
	}
	for _, m := range fresh {
		m.alerted, m.site = true, ip
	}
	ev.Diagnosis = strings.Join(notes, "; ")
	return ev, true
}

// siteRecoveries sends one recovery per alerted site once all its visors
// are back, or once the delay has passed since the first came back. Those
// still down then fall through to board alerts.
func (t *Tracker) siteRecoveries(now time.Time, delay time.Duration, order []*trackState) []Event {
	ips := make([]string, 0, len(t.sites))
	for ip := range t.sites {
		ips = append(ips, ip)
	}
	sort.Strings(ips)
	var events []Event
	for _, ip := range ips {
		ss := t.sites[ip]
		var back, down []*trackState
		for _, st := range order {
			if st.site != ip {
				continue
			}
			if st.info.Online {
				back = append(back, st)
			} else {
				down = append(down, st)
			}
		}
		if len(back) == 0 {
			if len(down) == 0 {
				delete(t.sites, ip)
			}
			continue
		}
		if ss.backAt.IsZero() {
			ss.backAt = now
		}
		if len(down) > 0 && now.Sub(ss.backAt) < delay {
			continue
		}
		ev := Event{Kind: KindOnline, Scope: ScopeSite, Site: ip, Target: Target{PublicIP: ip}, Since: ss.since, At: now}
		var notes []string
		if len(down) > 0 {
			notes = append(notes, fmt.Sprintf("%d of %d visors still offline", len(down), len(back)+len(down)))
		}
		var rebooted, stayed int
		for _, st := range back {
			ev.Members = append(ev.Members, st.info)
			switch RecoveryReason(time.Duration(st.info.Uptime), now.Sub(st.downSince)) {
			case ReasonRebooted:
				rebooted++
			case ReasonStayedUp:
				stayed++
			}
			if n := storageNote(st.info.Storage, t.diskWarn(), "reports storage problems"); n != "" {
				notes = append(notes, displayName(st.info)+" "+n)
			}
			st.downSince, st.alerted, st.site = time.Time{}, false, ""
		}
		switch {
		case rebooted > 0 && stayed == 0:
			ev.RecoveryReason = ReasonRebooted
		case stayed > 0 && rebooted == 0:
			ev.RecoveryReason = ReasonStayedUp
		case rebooted > 0:
			ev.RecoveryReason = fmt.Sprintf("%d of %d visors rebooted during the outage, the rest stayed up", rebooted, rebooted+stayed)
		}
		ev.Diagnosis = strings.Join(notes, "; ")
		for _, st := range down {
			st.alerted, st.site = false, ""
		}
		delete(t.sites, ip)
		events = append(events, ev)
	}
	return events
}

// RecoveryReason says whether a board rebooted during an outage of the
// given length: an uptime shorter than the outage means it booted after
// it went down. Empty when the uptime is unknown.
func RecoveryReason(uptime, outage time.Duration) string {
	switch {
	case uptime <= 0:
		return ""
	case uptime < outage:
		return ReasonRebooted
	default:
		return ReasonStayedUp
	}
}

type warning struct{ key, text string }

// storageWarnings lists the storage problems in s. A disk warning already
// active clears only 5 points under the threshold, so it does not flap.
func storageWarnings(s *Storage, diskWarn float64, active map[string]string) []warning {
	if s == nil {
		return nil
	}
	var out []warning
	if s.ReadOnlyRoot {
		out = append(out, warning{"read-only", "root filesystem is read-only"})
	}
	if s.IOErrors > 0 {
		text := fmt.Sprintf("%d storage I/O errors in the kernel log", s.IOErrors)
		if s.LastIOError != "" {
			text += ", last: " + s.LastIOError
		}
		out = append(out, warning{"io-errors", text})
	}
	limit := diskWarn
	if _, ok := active["disk"]; ok {
		limit -= 5
	}
	if s.DiskUsedPercent >= limit {
		out = append(out, warning{"disk", fmt.Sprintf("disk %.0f%% full", s.DiskUsedPercent)})
	}
	return out
}

func storageNote(s *Storage, diskWarn float64, prefix string) string {
	ws := storageWarnings(s, diskWarn, nil)
	if len(ws) == 0 {
		return ""
	}
	texts := make([]string, len(ws))
	for i, w := range ws {
		texts[i] = w.text
	}
	return prefix + ": " + strings.Join(texts, ", ")
}

func displayName(t Target) string {
	if t.Hostname != "" {
		return t.Hostname
	}
	return t.PK.String()
}

// mergeInfo keeps known details that a check did not report.
func mergeInfo(old, cur Target) Target {
	if cur.Hostname == "" {
		cur.Hostname = old.Hostname
	}
	if cur.PublicIP == "" {
		cur.PublicIP = old.PublicIP
	}
	if cur.LocalIP == "" {
		cur.LocalIP = old.LocalIP
	}
	if cur.LastSeen.IsZero() {
		cur.LastSeen = old.LastSeen
	}
	if cur.Storage == nil {
		cur.Storage = old.Storage
	}
	return cur
}

// Saved lists the state worth keeping across a restart.
func (t *Tracker) Saved() map[cipher.PubKey]Saved {
	out := map[cipher.PubKey]Saved{}
	for pk, st := range t.states {
		if !st.alerted && !st.lostAlerted && len(st.storage) == 0 {
			continue
		}
		s := Saved{Site: st.site}
		if st.lostAlerted {
			s.Lost = st.lostSince
		}
		if st.alerted {
			s.Since = st.downSince
		}
		for k := range st.storage {
			s.Storage = append(s.Storage, k)
		}
		sort.Strings(s.Storage)
		out[pk] = s
	}
	return out
}

// States lists every tracked visor, sorted by public key.
func (t *Tracker) States() []TargetState {
	out := make([]TargetState, 0, len(t.states))
	for _, st := range t.sorted() {
		ts := TargetState{Target: st.info, OfflineSince: st.downSince, Alerted: st.alerted, Site: st.site,
			LinkLostSince: st.lostSince, LinkAlerted: st.lostAlerted}
		for k, text := range st.storage {
			if text == "" {
				text = k
			}
			ts.StorageWarnings = append(ts.StorageWarnings, text)
		}
		sort.Strings(ts.StorageWarnings)
		out = append(out, ts)
	}
	return out
}
