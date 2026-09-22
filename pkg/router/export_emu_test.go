// Package router pkg/router/export_emu_test.go c2-net-routing
//
// The emulated testbed, exported to the package's EXTERNAL test package.
//
// The harness (harness_emu_test.go) has to live in package router — a leg is a
// (rg.tps, rg.fwd, rg.rvs, rg.mux) tuple and none of those are exported. But
// the stream bench in emu_stream_test.go drives a real pkg/skysocks Client over
// the same rig, and pkg/skysocks imports pkg/router, so that file can only be
// compiled as package router_test. This is the seam: a handful of exported
// wrappers around the harness, visible to router_test and to nothing else,
// because a _test.go file is not part of the package's public API.
package router

import (
	"net"
	"testing"

	"github.com/skycoin/skywire/pkg/router/emu"
)

// EmuLegSpec, EmuOpts, EmuRig and EmuLeg are the harness types under names the
// external test package can spell.
type (
	// EmuLegSpec is one emulated leg of a group.
	EmuLegSpec = emuLegSpec
	// EmuOpts configures a rig.
	EmuOpts = emuOpts
	// EmuRig is a running two-endpoint emulated group.
	EmuRig = emuRig
	// EmuLeg addresses one leg's two directions.
	EmuLeg = emuLeg
)

// NewEmuRig builds a rig. See newEmuRig.
func NewEmuRig(t *testing.T, opts EmuOpts) *EmuRig { return newEmuRig(t, opts) }

// ClientConn is the INITIATOR end of the group as a net.Conn — the tunnel a
// skysocks Client wraps in a yamux session.
func (r *emuRig) ClientConn() net.Conn { return r.A.rg }

// ExitConn is the ACCEPTOR end as a net.Conn — the tunnel a skysocks Server
// answers on.
func (r *emuRig) ExitConn() net.Conn { return r.B.rg }

// Legs is how many legs the rig was built with.
func (r *emuRig) Legs() int { return len(r.legs) }

// LegName is leg i's label in a report row.
func (r *emuRig) LegName(i int) string { return r.legs[i].Name }

// DownWireBytes is what the ACCEPTOR put on leg i — the download direction,
// retransmits included. It is the wire ground truth for a byte-share line: it
// counts what the emulated link carried, not what a ledger says it planned.
func (r *emuRig) DownWireBytes(i int) uint64 { return r.B.conns[i].Egress().Stats().SentBytes }

// UpWireBytes is what the INITIATOR put on leg i — the upload direction.
func (r *emuRig) UpWireBytes(i int) uint64 { return r.A.conns[i].Egress().Stats().SentBytes }

// WireBytesDir sums one direction over every leg of the rig.
func (r *emuRig) WireBytesDir(down bool) uint64 {
	var t uint64
	for i := range r.legs {
		if down {
			t += r.DownWireBytes(i)
		} else {
			t += r.UpWireBytes(i)
		}
	}
	return t
}

// EmuLinkConfig is emu.LinkConfig, re-exported so a bench file does not need
// its own import of the emulator to describe a leg.
type EmuLinkConfig = emu.LinkConfig
