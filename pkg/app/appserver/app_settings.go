// Package appserver pkg/app/appserver/app_settings.go
//
// Per-app live tuning settings — the one visor→app value channel the mux bench
// needs, built the only way the existing app RPC allows.
//
// The app is the RPC client and the visor the server (rpc_ingress_gateway.go),
// so there is no way to PUSH a value into a running app. Instead the visor
// holds a map per app name and the app PULLS it on a tick it already runs
// (skysocks-client's keepalive loop). A version counter makes the steady state
// an empty round-trip: the app reports the version it has applied, and the
// gateway answers with values only when the visor's is newer.
//
// Two payloads travel together: the int64 map (pkg/skysocks/skysettings encodes
// every numeric, duration, ratio, bool and enum knob into it) and a string map
// for the LIST knobs — a set of public keys, a set of transport types — whose
// payload does not fit an int64. One version covers both.
//
// The store is process-scoped but NOT cleared when an app stops: an operator
// who set a knob meant it for the app, not for one run of its process, and the
// bench spent a release cycle re-applying its sweep after every restart. The
// visor persists the same map to its config (visorconfig.V1.AppSettings) and
// restores it at boot, so `proxy settings --reset` is the one thing that
// forgets a knob.
//
// Ops are the edge-triggered half of the same channel. A value is a level —
// it says what the app should BE — and some things an operator asks for are
// not levels at all: "cut the tunnel on port 49170" happens once. Those ride
// the same pull as a small sequenced queue, applied in order and acked by
// sequence, so a missed tick delays an op but never loses or repeats it.
package appserver

import "sync"

// appSettings is the visor's per-app knob store. Values are the int64 payloads
// of pkg/skysocks/skysettings and text the rendered list ones — the gateway
// neither parses nor validates either, it just carries them; the app's own
// registry ignores names it does not know.
type appSettings struct {
	mx sync.RWMutex
	// values is the FULL intended set per app: a knob missing from it means
	// "compiled default", so a reset is a delete rather than a sentinel.
	values map[string]map[string]int64
	// text is the same for the list knobs.
	text map[string]map[string]string
	// version is bumped on every change, applied is the version the app last
	// told us it had installed. version != applied is the CLI's "pending".
	version map[string]uint64
	applied map[string]uint64
	// ops is the pending op queue per app, oldest first, and opSeq the
	// sequence they are stamped with. An op leaves the queue once the app
	// reports having applied its sequence.
	ops   map[string][]AppOp
	opSeq map[string]uint64
}

// AppOp is one edge-triggered instruction for a running app: do this once,
// now. Kind names it, Arg carries its single parameter (the route group's port
// for AppOpCutTunnel), Seq orders it and is what the app acks.
type AppOp struct {
	Seq  uint64 `json:"seq"`
	Kind string `json:"kind"`
	Arg  int64  `json:"arg,omitempty"`
}

// AppOpCutTunnel closes exactly one of the app's tunnels — the one dialed from
// the local port in Arg, which is the port `proxy mux info` prints as the
// route group's dst_port. The app's own pool replaces it, which is the point:
// it is the per-tunnel teardown a bench previously had to fake by cutting the
// host's transport.
const AppOpCutTunnel = "cut-tunnel"

// AppOpConsumedTunnel tells the app that one of its tunnels was SPENT, not
// lost: the router re-homed that tunnel's whole route chain into an active
// group as a mux leg (docs/design/leg-rehome.md), and the tunnel's own group is
// closing as a result. Arg is the route group's port, exactly as for
// AppOpCutTunnel. The app drops the tunnel from its pool and records
// tunnel_consumed, WITHOUT the redial backoff reset and pool-fill arming a
// death triggers — nobody lost this tunnel, so nothing needs replacing in a
// hurry.
const AppOpConsumedTunnel = "consumed-tunnel"

func newAppSettings() *appSettings {
	return &appSettings{
		values:  make(map[string]map[string]int64),
		text:    make(map[string]map[string]string),
		version: make(map[string]uint64),
		applied: make(map[string]uint64),
		ops:     make(map[string][]AppOp),
		opSeq:   make(map[string]uint64),
	}
}

// pull answers the app's poll: it records the version the app says it has
// applied, drops the ops it has acked, and returns the current values plus the
// version they carry. The values are nil when the app is already up to date, so
// a steady-state poll costs an empty gob round-trip.
func (s *appSettings) pull(appName string, applied, opsApplied uint64) (map[string]int64, map[string]string, []AppOp, uint64) {
	s.mx.Lock()
	defer s.mx.Unlock()
	s.applied[appName] = applied
	if opsApplied > 0 {
		s.dropAckedOpsLocked(appName, opsApplied)
	}
	ops := s.ops[appName]
	if len(ops) > 0 {
		ops = append([]AppOp(nil), ops...)
	} else {
		ops = nil
	}
	v := s.version[appName]
	if applied == v {
		return nil, nil, ops, v
	}
	return copyValues(s.values[appName]), copyText(s.text[appName]), ops, v
}

func (s *appSettings) dropAckedOpsLocked(appName string, acked uint64) {
	pending := s.ops[appName]
	keep := pending[:0]
	for _, op := range pending {
		if op.Seq > acked {
			keep = append(keep, op)
		}
	}
	if len(keep) == 0 {
		delete(s.ops, appName)
		return
	}
	s.ops[appName] = keep
}

// set replaces the whole intended set for appName and returns the new version.
// Empty or nil maps are a full reset.
func (s *appSettings) set(appName string, vals map[string]int64, text map[string]string) uint64 {
	s.mx.Lock()
	defer s.mx.Unlock()
	if len(vals) == 0 {
		delete(s.values, appName)
	} else {
		s.values[appName] = copyValues(vals)
	}
	if len(text) == 0 {
		delete(s.text, appName)
	} else {
		s.text[appName] = copyText(text)
	}
	s.version[appName]++
	return s.version[appName]
}

// queueOp appends one op for appName and returns its sequence.
func (s *appSettings) queueOp(appName, kind string, arg int64) uint64 {
	s.mx.Lock()
	defer s.mx.Unlock()
	s.opSeq[appName]++
	seq := s.opSeq[appName]
	s.ops[appName] = append(s.ops[appName], AppOp{Seq: seq, Kind: kind, Arg: arg})
	return seq
}

// state is the CLI's read: the intended values, the version they carry, and
// the version the app last reported applying.
func (s *appSettings) state(appName string) (map[string]int64, map[string]string, uint64, uint64) {
	s.mx.RLock()
	defer s.mx.RUnlock()
	return copyValues(s.values[appName]), copyText(s.text[appName]), s.version[appName], s.applied[appName]
}

// all is the visor's read for persistence: every app's intended set.
func (s *appSettings) all() (map[string]map[string]int64, map[string]map[string]string) {
	s.mx.RLock()
	defer s.mx.RUnlock()
	vals := make(map[string]map[string]int64, len(s.values))
	for app, v := range s.values {
		vals[app] = copyValues(v)
	}
	text := make(map[string]map[string]string, len(s.text))
	for app, t := range s.text {
		text[app] = copyText(t)
	}
	return vals, text
}

func copyValues(in map[string]int64) map[string]int64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyText(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// AppSettingsResp is what an app gets back from the AppSettings ingress call.
type AppSettingsResp struct {
	// Version is the visor's current version for this app. The app stores it
	// and reports it as Applied on the next poll.
	Version uint64 `json:"version"`
	// Values is the full intended set, or nil when Applied already matched
	// Version (nothing to do) — NOT "reset everything", which is an empty
	// non-nil map carried at a NEW version.
	Values map[string]int64 `json:"values,omitempty"`
	// Text is the same for the list knobs.
	Text map[string]string `json:"text,omitempty"`
	// Ops are the edge-triggered instructions still pending for this app,
	// oldest first. Unlike Values they are carried on EVERY poll until the app
	// acks their sequence, so one lost answer costs a tick, not the op.
	Ops []AppOp `json:"ops,omitempty"`
	// Changed distinguishes the two nil cases above.
	Changed bool `json:"changed"`
}

// AppSettingsReq is an app polling for its settings, reporting the version it
// currently has installed and the highest op sequence it has applied.
type AppSettingsReq struct {
	Applied    uint64 `json:"applied"`
	OpsApplied uint64 `json:"ops_applied,omitempty"`
}

// SetAppSettings replaces the live tuning knobs held for appName and returns
// the new version. Implements ProcManager.
func (m *procManager) SetAppSettings(appName string, vals map[string]int64, text map[string]string) uint64 {
	return m.settings.set(appName, vals, text)
}

// QueueAppOp queues one edge-triggered op for appName. Implements ProcManager.
func (m *procManager) QueueAppOp(appName, kind string, arg int64) uint64 {
	return m.settings.queueOp(appName, kind, arg)
}

// AllAppSettings returns every app's intended set. Implements ProcManager.
func (m *procManager) AllAppSettings() (map[string]map[string]int64, map[string]map[string]string) {
	return m.settings.all()
}

// AppSettings answers an app's poll for its knobs. Implements ProcManager.
func (m *procManager) AppSettings(appName string, applied, opsApplied uint64) (map[string]int64, map[string]string, []AppOp, uint64) {
	return m.settings.pull(appName, applied, opsApplied)
}

// AppSettingsState reports the intended values, their version, and the version
// the app last acknowledged. Implements ProcManager.
func (m *procManager) AppSettingsState(appName string) (map[string]int64, map[string]string, uint64, uint64) {
	return m.settings.state(appName)
}
