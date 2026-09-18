// Package emu pkg/router/emu/report.go
package emu

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// LegShare is one leg's contribution to a transfer.
type LegShare struct {
	Index        int
	Name         string
	SentBytes    uint64
	RecvBytes    uint64
	PayloadBytes uint64 // unique payload this leg was the first to deliver
	Retransmits  uint64
	ProbeOnly    bool
	Standby      bool
	RttMs        float64
}

// Share is this leg's fraction of the group's unique payload, 0..1.
func (l LegShare) Share(total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(l.PayloadBytes) / float64(total)
}

// Summary is one scenario run, in the same shape the live bench rows carry so
// an emulated result and a rig result can be read side by side.
type Summary struct {
	Name    string
	Dir     string // "down" (acceptor sends) or "up" (initiator sends)
	Bytes   int64  // application bytes offered
	Got     int64  // application bytes received
	Elapsed time.Duration
	HashOK  bool

	WireBytes       uint64 // bytes put on every leg, retransmits included
	Retransmits     uint64
	ReorderDrops    uint64
	SendWindowWaits uint64
	SacksSent       uint64
	SacksRecv       uint64

	// TTFB is time to the first application byte. For a mid-transfer cut it
	// is measured from the cut to the next byte that lands after it.
	TTFB time.Duration

	Legs   []LegShare
	Events []string
	Notes  []string
}

// GoodputBps is application bytes received per second.
func (s Summary) GoodputBps() float64 {
	if s.Elapsed <= 0 {
		return 0
	}
	return float64(s.Got) / s.Elapsed.Seconds()
}

// WireRatio is wire bytes over application bytes: 1.0 is a perfect transfer,
// and everything above it is header, retransmit and repair overhead.
func (s Summary) WireRatio() float64 {
	if s.Got == 0 {
		return 0
	}
	return float64(s.WireBytes) / float64(s.Got)
}

// PayloadTotal is the unique payload credited across the legs.
func (s Summary) PayloadTotal() uint64 {
	var t uint64
	for _, l := range s.Legs {
		t += l.PayloadBytes
	}
	return t
}

// Table renders the summary as the fixed-width block every scenario prints,
// so two runs are comparable by eye.
func (s Summary) Table() string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n%-28s %-5s %9s %12s %8s %7s %6s %7s %7s\n",
		"scenario", "dir", "bytes", "goodput B/s", "seconds", "wire/gp", "hash", "retx", "ttfb_ms")
	fmt.Fprintf(&b, "%-28s %-5s %9d %12.0f %8.2f %7.3f %6v %7d %7.0f\n",
		trunc(s.Name, 28), s.Dir, s.Bytes, s.GoodputBps(), s.Elapsed.Seconds(),
		s.WireRatio(), s.HashOK, s.Retransmits, float64(s.TTFB)/float64(time.Millisecond))

	total := s.PayloadTotal()
	fmt.Fprintf(&b, "  %-4s %-18s %10s %10s %10s %6s %6s %8s\n",
		"leg", "name", "sent", "recv", "payload", "share", "retx", "state")
	legs := append([]LegShare(nil), s.Legs...)
	sort.Slice(legs, func(i, j int) bool { return legs[i].Index < legs[j].Index })
	for _, l := range legs {
		state := "active"
		switch {
		case l.ProbeOnly:
			state = "probe"
		case l.Standby:
			state = "standby"
		}
		fmt.Fprintf(&b, "  %-4d %-18s %10d %10d %10d %5.1f%% %6d %8s\n",
			l.Index, trunc(l.Name, 18), l.SentBytes, l.RecvBytes, l.PayloadBytes,
			100*l.Share(total), l.Retransmits, state)
	}
	if s.ReorderDrops > 0 || s.SendWindowWaits > 0 || s.SacksSent > 0 || s.SacksRecv > 0 {
		fmt.Fprintf(&b, "  reorder_drops=%d send_window_waits=%d sacks_sent=%d sacks_recv=%d\n",
			s.ReorderDrops, s.SendWindowWaits, s.SacksSent, s.SacksRecv)
	}
	for _, n := range s.Notes {
		fmt.Fprintf(&b, "  note: %s\n", n)
	}
	for _, e := range s.Events {
		fmt.Fprintf(&b, "  event: %s\n", e)
	}
	return b.String()
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
