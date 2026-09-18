// Package cliroute is the output shape of the `skywire cli route` commands.
//
// It lives here rather than in a function body so callers, and the e2e suite,
// can import it. The suite already runs `route --json`, `route rule --json`
// and `route add-rule ... --json` and parses what comes back.
package cliroute

import (
	"fmt"
	"io"
	"sort"
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

	// Shared-bottleneck detection.
	SBDMinSamples      int     `json:"sbd_min_samples"`
	SBDSampleInterval  string  `json:"sbd_sample_interval"`
	SBDTrialWindow     string  `json:"sbd_trial_window"`
	SBDTrialLoss       float64 `json:"sbd_trial_loss"`
	SBDBackoff         string  `json:"sbd_backoff"`
	SBDMinEvidenceRate int64   `json:"sbd_min_evidence_rate"`
	// ForwardSpill reports whether a forward frame may leave the confined leg when
	// that leg is at its send window; ForwardSwitchMargin is how much lower a
	// challenger leg must measure, for two consecutive samples, to take the
	// direction.
	ForwardSpill        bool    `json:"forward_spill"`
	ForwardSwitchMargin float64 `json:"forward_switch_margin"`

	// LegStarveRatio is how many times the best ready leg's delay basis a leg's own
	// must exceed before the scheduler cuts it to LegProbeBytes per window instead
	// of a proportional share.
	LegStarveRatio float64 `json:"leg_starve_ratio"`
	LegProbeBytes  int64   `json:"leg_probe_bytes"`

	// SBDDemote reports whether a shared-bottleneck ruling may park a leg at all.
	// False (the default) means rulings are recorded as sbd_ruling mux events and
	// nothing is demoted.
	SBDDemote bool `json:"sbd_demote"`

	// Knobs is the WHOLE router knob catalog as a stable map of catalog name to
	// formatted value — the shape a bench runner saves before a sweep and feeds
	// back verbatim afterwards (`route settings k=v …`). It is the complete
	// surface; the typed fields above are the same values under their older
	// names, kept so existing consumers do not break.
	Knobs map[string]string `json:"knobs"`

	// KnobDetail carries each knob's compiled default, whether it was explicitly
	// set, and what the visor config holds for it — the persisted-vs-live view.
	KnobDetail map[string]Knob `json:"knob_detail,omitempty"`

	// AppKnobs is every per-app override map (`route settings --app <name>`),
	// keyed by app name. Absent when no app has overrides.
	AppKnobs map[string]map[string]string `json:"app_knobs,omitempty"`
}

// Knob is one row of Settings.KnobDetail.
type Knob struct {
	Kind      string `json:"kind"`
	Value     string `json:"value"`
	Default   string `json:"default"`
	Set       bool   `json:"set"`
	Persisted string `json:"persisted,omitempty"`
	Doc       string `json:"doc,omitempty"`
}

// Human writes one knob per line.
func (s Settings) Human(w io.Writer) error {
	_, err := fmt.Fprintf(w, "min_hops: %d\nexisting_tp_only: %v\nforce_local_routes: %v\ntransport_preference: %s\n"+
		"ecf_max_window_bytes: %d\necf_min_window_bytes: %d\necf_window_margin: %g\n"+
		"send_window_wait_max: %s\nleg_park_min_hold: %s\n"+
		"dead_route_hold: %s\ndead_route_hold_max: %s\nmux_fec: %v\n"+
		"sbd_min_samples: %d\nsbd_sample_interval: %s\n"+
		"sbd_trial_window: %s\nsbd_trial_loss: %g\nsbd_backoff: %s\n"+
		"sbd_min_evidence_rate: %d\nsbd_demote: %v\n"+
		"forward_spill: %v\nforward_switch_margin: %g\n"+
		"leg_starve_ratio: %g\nleg_probe_bytes: %d\n",
		s.MinHops, s.ExistingTPOnly, s.ForceLocalRoutes, strings.Join(s.TransportPreference, ","),
		s.EcfMaxWindowBytes, s.EcfMinWindowBytes, s.EcfWindowMargin,
		s.SendWindowWaitMax, s.LegParkMinHold, s.DeadRouteHold, s.DeadRouteHoldMax, s.MuxFEC,
		s.SBDMinSamples, s.SBDSampleInterval,
		s.SBDTrialWindow, s.SBDTrialLoss, s.SBDBackoff, s.SBDMinEvidenceRate, s.SBDDemote,
		s.ForwardSpill, s.ForwardSwitchMargin,
		s.LegStarveRatio, s.LegProbeBytes)
	if err != nil {
		return err
	}
	// …then the whole catalog, name-sorted, so the human output is the same
	// surface the JSON carries rather than a curated subset of it.
	names := make([]string, 0, len(s.Knobs))
	for n := range s.Knobs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if _, err := fmt.Fprintf(w, "%s: %s\n", n, s.Knobs[n]); err != nil {
			return err
		}
	}
	apps := make([]string, 0, len(s.AppKnobs))
	for a := range s.AppKnobs {
		apps = append(apps, a)
	}
	sort.Strings(apps)
	for _, a := range apps {
		keys := make([]string, 0, len(s.AppKnobs[a]))
		for n := range s.AppKnobs[a] {
			keys = append(keys, n)
		}
		sort.Strings(keys)
		for _, n := range keys {
			if _, err := fmt.Fprintf(w, "app[%s] %s: %s\n", a, n, s.AppKnobs[a][n]); err != nil {
				return err
			}
		}
	}
	return nil
}
