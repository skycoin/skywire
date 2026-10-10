package api

import (
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"unsafe"
	"weak"

	kcp "github.com/0magnet/kcp-go/v5"
)

// sudphSessions follows every session the SUDPH listener hands out, so a
// session that outlives its handler shows up on GET /sudph-sessions.
type sudphSessions struct {
	accepted atomic.Int64
	closed   atomic.Int64
	lis      atomic.Pointer[kcp.Listener]

	mu sync.Mutex
	m  map[*sessionMark]weak.Pointer[kcp.UDPSession]
}

type sessionMark struct {
	closed  atomic.Bool
	wrapper weak.Pointer[trackedConn]
}

type trackedConn struct {
	net.Conn
	mark *sessionMark
	ss   *sudphSessions
}

func (c *trackedConn) Close() error {
	if c.mark.closed.CompareAndSwap(false, true) {
		c.ss.closed.Add(1)
	}
	return c.Conn.Close()
}

func (ss *sudphSessions) track(c net.Conn) net.Conn {
	ss.accepted.Add(1)
	s, ok := c.(*kcp.UDPSession)
	if !ok {
		return c
	}
	mark := &sessionMark{}
	tc := &trackedConn{Conn: c, mark: mark, ss: ss}
	mark.wrapper = weak.Make(tc)
	ss.mu.Lock()
	if ss.m == nil {
		ss.m = make(map[*sessionMark]weak.Pointer[kcp.UDPSession])
	}
	if len(ss.m)%4096 == 4095 {
		ss.pruneLocked()
	}
	ss.m[mark] = weak.Make(s)
	ss.mu.Unlock()
	return tc
}

func (ss *sudphSessions) pruneLocked() {
	for m, wp := range ss.m {
		if wp.Value() == nil {
			delete(ss.m, m)
		}
	}
}

// SudphSessionsReport counts the SUDPH sessions still in memory.
type SudphSessionsReport struct {
	Accepted int64 `json:"accepted"`
	Closed   int64 `json:"closed"`
	// InMemory are accepted sessions not yet collected, split by whether
	// this API closed them and whether its wrapper is still reachable.
	InMemory          int `json:"in_memory"`
	InMemoryClosed    int `json:"in_memory_closed"`
	InMemoryWrapperUp int `json:"in_memory_wrapper_reachable"`
	// KCP counters are process wide, so they include the visor's own sessions.
	// ListenerSessions is the size of the KCP listener's own session map, and
	// ClosedInListener how many sessions this API closed are still in it.
	ListenerSessions int    `json:"kcp_listener_sessions"`
	ClosedInListener int    `json:"closed_in_listener_map"`
	KCPCurrEstab     uint64 `json:"kcp_curr_estab"`
	KCPPassiveOpens  uint64 `json:"kcp_passive_opens"`
	KCPActiveOpens   uint64 `json:"kcp_active_opens"`
	SudphConnsBound  int    `json:"sudph_conns_bound"`
}

func (a *API) sudphSessionsReport() SudphSessionsReport {
	ss := &a.sudphSessions
	r := SudphSessionsReport{Accepted: ss.accepted.Load(), Closed: ss.closed.Load()}
	n, inMap, _ := listenerSessions(ss.lis.Load())
	r.ListenerSessions = n
	ss.mu.Lock()
	ss.pruneLocked()
	for m, wp := range ss.m {
		r.InMemory++
		if m.closed.Load() {
			r.InMemoryClosed++
			if s := wp.Value(); s != nil && inMap[uintptr(unsafe.Pointer(s))] { //nolint:gosec
				r.ClosedInListener++
			}
		}
		if m.wrapper.Value() != nil {
			r.InMemoryWrapperUp++
		}
	}
	ss.mu.Unlock()
	snmp := kcp.DefaultSnmp.Copy()
	r.KCPCurrEstab, r.KCPPassiveOpens, r.KCPActiveOpens = snmp.CurrEstab, snmp.PassiveOpens, snmp.ActiveOpens
	a.udpConnsMu.RLock()
	r.SudphConnsBound = len(a.udpConns)
	a.udpConnsMu.RUnlock()
	return r
}

func (a *API) sudphSessionsHandler(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, r, http.StatusOK, a.sudphSessionsReport())
}
