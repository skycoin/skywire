// Package router pkg/router/router_intake.go c3-rtr-core
//
// Intake diagnostics for `visor state`: what the router's single inbound
// packet loop is doing with what the transports hand it. Counted here rather
// than only logged so a stalled or misdelivered path shows up in a snapshot.
package router

import (
	"sync"

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
	// RouteGroupQueues is the inbound app queue of every active route group:
	// a queue at capacity is an app that stopped reading, and the router
	// blocks on it (up to 30 s per packet) — stalling every transport.
	RouteGroupQueues []RouteGroupQueue `json:"route_group_queues,omitempty"`
}

// RouteGroupQueue is one route group's inbound queue depth.
type RouteGroupQueue struct {
	Desc     string `json:"desc"`
	App      string `json:"app,omitempty"`
	Queue    int    `json:"queue"`
	Capacity int    `json:"capacity"`
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
		out.RouteGroupQueues = append(out.RouteGroupQueues, RouteGroupQueue{
			Desc:     rg.desc.String(),
			App:      rg.AppName(),
			Queue:    q,
			Capacity: c,
		})
	}
	return out
}

// readQueue is the depth and capacity of the group's inbound app queue.
func (rg *RouteGroup) readQueue() (int, int) {
	return len(rg.readCh), cap(rg.readCh)
}
