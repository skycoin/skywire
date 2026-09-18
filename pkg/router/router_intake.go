// Package router pkg/router/router_intake.go c3-rtr-core
//
// Intake diagnostics for `visor state`: what the router's single inbound
// packet loop is doing with what the transports hand it. Counted here rather
// than only logged so a stalled or misdelivered path shows up in a snapshot.
package router

import (
	"sync"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// IntakeStats is the router's inbound-path view.
type IntakeStats struct {
	// UnknownPacketTypes counts packets dropped as ErrUnknownPacketType, by
	// type name. Non-zero means a peer sends a type this build does not
	// route, or a transport lost its route-ID-0 handler (#4725).
	UnknownPacketTypes map[string]int64 `json:"unknown_packet_types,omitempty"`
	// ControlPlaneToRouter counts route-ID-0 control packets that reached
	// the router instead of being intercepted on the transport, by type.
	ControlPlaneToRouter map[string]int64 `json:"control_plane_to_router,omitempty"`
	// StaleRouteDrops counts frames for rules that no longer exist (normal
	// during teardown; a climbing count on an idle visor is not).
	StaleRouteDrops int64 `json:"stale_route_drops"`
	// RouteGroupQueues is the two inbound queues of every active route group:
	// the app queue (queue/capacity) and the intake queue between the router's
	// shared read loop and the group's own worker (intake_*).
	//
	// An app queue at capacity is an app that stopped reading. That used to
	// block the shared loop — up to 30 s per packet, stalling every transport
	// on the visor; now it backs up into that group's intake queue instead,
	// and a climbing intake_drops is where the loss shows up. Non-zero
	// intake_drops on one group and healthy transfers elsewhere is the
	// expected shape of a single wedged app.
	RouteGroupQueues []RouteGroupQueue `json:"route_group_queues,omitempty"`
	// ForwardQueues is the TRANSIT write side: one entry per next-hop
	// transport this visor is relaying frames to. Non-zero drops name a peer
	// that stopped draining; they used to be a visor-wide freeze instead
	// (see router_forward.go).
	ForwardQueues []ForwardQueue `json:"forward_queues,omitempty"`
}

// RouteGroupQueue is one route group's inbound queue depths.
type RouteGroupQueue struct {
	Desc     string `json:"desc"`
	App      string `json:"app,omitempty"`
	Queue    int    `json:"queue"`
	Capacity int    `json:"capacity"`
	// IntakeQueue/IntakeCapacity are the group's intake queue (see
	// RouteGroup.inCh), and IntakeDrops counts packets the router dropped
	// because it was full.
	IntakeQueue    int    `json:"intake_queue"`
	IntakeCapacity int    `json:"intake_capacity"`
	IntakeDrops    uint64 `json:"intake_drops"`
}

type intakeCounters struct {
	mx      sync.Mutex
	unknown map[string]int64
	control map[string]int64
	stale   int64
}

func (c *intakeCounters) noteUnknown(t routing.PacketType) {
	c.mx.Lock()
	if c.unknown == nil {
		c.unknown = make(map[string]int64)
	}
	c.unknown[t.String()]++
	c.mx.Unlock()
}

func (c *intakeCounters) noteControl(t routing.PacketType) {
	c.mx.Lock()
	if c.control == nil {
		c.control = make(map[string]int64)
	}
	c.control[t.String()]++
	c.mx.Unlock()
}

func (c *intakeCounters) noteStale() {
	c.mx.Lock()
	c.stale++
	c.mx.Unlock()
}

func (c *intakeCounters) snapshot() (unknown, control map[string]int64, stale int64) {
	c.mx.Lock()
	defer c.mx.Unlock()
	unknown = make(map[string]int64, len(c.unknown))
	for k, v := range c.unknown {
		unknown[k] = v
	}
	control = make(map[string]int64, len(c.control))
	for k, v := range c.control {
		control[k] = v
	}
	return unknown, control, c.stale
}

// IntakeStats snapshots the router's inbound-path counters and every active
// route group's inbound queue depth. Reached through a type assertion from
// the visor so the Router interface (and its mock) stay unchanged.
func (r *router) IntakeStats() IntakeStats {
	unknown, control, stale := r.intake.snapshot()
	out := IntakeStats{StaleRouteDrops: stale}
	out.ForwardQueues = r.forward.snapshot()
	if len(unknown) > 0 {
		out.UnknownPacketTypes = unknown
	}
	if len(control) > 0 {
		out.ControlPlaneToRouter = control
	}
	r.mx.Lock()
	rgs := make([]*RouteGroup, 0, len(r.rgsNs)+len(r.rgsRaw))
	for _, nrg := range r.rgsNs {
		if nrg != nil && nrg.rg != nil {
			rgs = append(rgs, nrg.rg)
		}
	}
	for _, rg := range r.rgsRaw {
		if rg != nil {
			rgs = append(rgs, rg)
		}
	}
	r.mx.Unlock()
	for _, rg := range rgs {
		q, c := rg.readQueue()
		iq, ic, drops := rg.intakeQueue()
		out.RouteGroupQueues = append(out.RouteGroupQueues, RouteGroupQueue{
			Desc:           rg.desc.String(),
			App:            rg.AppName(),
			Queue:          q,
			Capacity:       c,
			IntakeQueue:    iq,
			IntakeCapacity: ic,
			IntakeDrops:    drops,
		})
	}
	return out
}

// readQueue is the depth and capacity of the group's inbound app queue.
func (rg *RouteGroup) readQueue() (int, int) {
	return len(rg.readCh), cap(rg.readCh)
}

// ForwardQueue is one next-hop transport's transit write queue: what this
// visor is relaying for other people's routes, and what it had to drop. A
// climbing DropsQueueFull or DropsWriteTimeout on one transport with healthy
// transfers elsewhere is the expected shape of a single wedged transit peer —
// which is exactly what it must look like, since that peer used to take the
// whole dataplane down with it.
type ForwardQueue struct {
	TpID     uuid.UUID     `json:"tp_id"`
	TpType   string        `json:"tp_type,omitempty"`
	Remote   cipher.PubKey `json:"remote_pk,omitempty"`
	Queue    int           `json:"queue"`
	Capacity int           `json:"capacity"`
	// QueuedBytes is what the frames waiting in it currently hold, against
	// forward.queue_bytes when that bound is set.
	QueuedBytes int64  `json:"queued_bytes"`
	Sent        uint64 `json:"sent"`
	// The named drop reasons, one counter each.
	DropsQueueFull    uint64 `json:"forward_drop_queue_full"`
	DropsWriteTimeout uint64 `json:"forward_drop_write_timeout"`
	DropsWriteError   uint64 `json:"forward_drop_write_error"`
}
