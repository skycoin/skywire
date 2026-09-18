// Package cliroute is the output shape of the `skywire cli route` commands.
//
// It lives here rather than in a function body so callers, and the e2e suite,
// can import it. The suite already runs `route --json`, `route rule --json`
// and `route add-rule ... --json` and parses what comes back.
package cliroute

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// Rule is one routing rule as `skywire cli route` reports it.
//
// The optional fields are per rule TYPE, not per flag: an app rule has ports
// and a remote key, a forward rule has a next hop, and neither carries the
// other's fields. omitempty is what keeps one type serving all three without a
// consumer having to recognize three documents.
type Rule struct {
	ID   routing.RouteID `json:"id"`
	Type string          `json:"type"`

	// App rules.
	LocalPort  string `json:"local_port,omitempty"`
	RemotePort string `json:"remote_port,omitempty"`
	RemotePK   string `json:"remote_pk,omitempty"`

	// Forward and intermediary-forward rules.
	NextRouteID string `json:"next_route_id,omitempty"`
	NextTpID    string `json:"next_transport_id,omitempty"`

	// ExpireAt keeps its hyphen: it is the name already emitted, and renaming
	// it would break every consumer parsing it today for the sake of tidiness.
	ExpireAt time.Duration `json:"expire-at"`
}

// MinHops is the result of setting the minimum hop count.
type MinHops struct {
	MinHops int `json:"min_hops"`
}

// Human writes the confirmation the command printed.
func (m MinHops) Human(w io.Writer) error {
	_, err := fmt.Fprintf(w, "Minimum hops set to %d\n", m.MinHops)
	return err
}

// PolicyBench times repeated evaluations of a routing policy script.
type PolicyBench struct {
	Script     string `json:"script"`
	Iterations int    `json:"iterations"`
	// Durations are strings because that is what they were printed as, and a
	// caller comparing runs wants the same token it saw in the terminal.
	Total   string `json:"total"`
	Average string `json:"average"`
	P50     string `json:"p50"`
	P99     string `json:"p99"`
}

// Human writes the aligned block the command printed.
func (p PolicyBench) Human(w io.Writer) error {
	_, err := fmt.Fprintf(w, "script:  %s\niters:   %d\ntotal:   %s\navg:     %s\np50:     %s\np99:     %s\n",
		p.Script, p.Iterations, p.Total, p.Average, p.P50, p.P99)
	return err
}

// TraceHop is one hop of `route trace`.
type TraceHop struct {
	Index int           `json:"hop"`
	PK    cipher.PubKey `json:"pk"`
	// RTT is nanoseconds for scripting; the human path renders milliseconds.
	RTT           time.Duration `json:"rtt_ns,omitempty"`
	IsDestination bool          `json:"destination"`
	Error         string        `json:"error,omitempty"`
}

// Trace is the whole path `route trace` measured. It was an anonymous struct
// marshaled inline, which is a shape no caller could name.
type Trace struct {
	Src  string     `json:"src"`
	Dst  string     `json:"dst"`
	Hops []TraceHop `json:"hops"`
}

// Settings is the visor-wide router knobs (`route settings`).
type Settings struct {
	ForceLocalRoutes    bool     `json:"force_local_routes"`
	ExistingTPOnly      bool     `json:"existing_tp_only"`
	MinHops             uint16   `json:"min_hops"`
	TransportPreference []string `json:"transport_preference"`

	// The mux send-window shape and the adaptive park hold.
	EcfMaxWindowBytes int64   `json:"ecf_max_window_bytes"`
	EcfMinWindowBytes int64   `json:"ecf_min_window_bytes"`
	EcfWindowMargin   float64 `json:"ecf_window_margin"`
	SendWindowWaitMax string  `json:"send_window_wait_max"`
	LegParkMinHold    string  `json:"leg_park_min_hold"`
	DeadRouteHold     string  `json:"dead_route_hold"`
	DeadRouteHoldMax  string  `json:"dead_route_hold_max"`
	MuxFEC            bool    `json:"mux_fec"`
}

// Human writes one knob per line.
func (s Settings) Human(w io.Writer) error {
	_, err := fmt.Fprintf(w, "min_hops: %d\nexisting_tp_only: %v\nforce_local_routes: %v\ntransport_preference: %s\n"+
		"ecf_max_window_bytes: %d\necf_min_window_bytes: %d\necf_window_margin: %g\n"+
		"send_window_wait_max: %s\nleg_park_min_hold: %s\n"+
		"dead_route_hold: %s\ndead_route_hold_max: %s\nmux_fec: %v\n",
		s.MinHops, s.ExistingTPOnly, s.ForceLocalRoutes, strings.Join(s.TransportPreference, ","),
		s.EcfMaxWindowBytes, s.EcfMinWindowBytes, s.EcfWindowMargin,
		s.SendWindowWaitMax, s.LegParkMinHold, s.DeadRouteHold, s.DeadRouteHoldMax, s.MuxFEC)
	return err
}
