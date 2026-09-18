// Package cliproxy pkg/cliout/cliproxy/mux_controls.go
//
// Output shapes for the per-leg mux controls: the weight override and the
// negotiated per-group view. Both answer a question rather than reporting a
// mutation, so unlike MuxOp they carry the whole state back, not just what
// changed.
package cliproxy

import (
	"fmt"
	"io"
	"text/tabwriter"
)

// MuxWeights is the weights state of one route group: the scheduler mode in
// force and, when it is the explicit one, the per-leg weights it spreads by.
type MuxWeights struct {
	App     string         `json:"app"`
	DstPort uint16         `json:"dst_port"`
	SrcPort uint16         `json:"src_port"`
	Mode    string         `json:"mode"`
	Weights []MuxLegWeight `json:"weights,omitempty"`
}

// MuxLegWeight is one leg's share, named by its first-hop transport.
type MuxLegWeight struct {
	TransportID string  `json:"transport_id"`
	Weight      float64 `json:"weight"`
}

// Human prints the mode and, beneath it, one row per weighted leg.
func (m MuxWeights) Human(w io.Writer) error {
	if _, err := fmt.Fprintf(w, "app=%s rg=%d mode=%s\n", m.App, m.DstPort, m.Mode); err != nil {
		return err
	}
	if len(m.Weights) == 0 {
		_, err := fmt.Fprintln(w, "no explicit weights installed")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "TRANSPORT\tWEIGHT"); err != nil {
		return err
	}
	for _, lw := range m.Weights {
		if _, err := fmt.Fprintf(tw, "%s\t%.4f\n", lw.TransportID, lw.Weight); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// MuxNegotiated is one route group's negotiated capabilities and the
// send-window shape in effect on it.
type MuxNegotiated struct {
	DstPort             uint16         `json:"dst_port"`
	SrcPort             uint16         `json:"src_port"`
	Remote              string         `json:"remote"`
	AppName             string         `json:"app_name,omitempty"`
	Legs                int            `json:"legs"`
	MuxEnabled          bool           `json:"mux_enabled"`
	SACKEnabled         bool           `json:"sack_enabled"`
	HOLRetxEnabled      bool           `json:"hol_retx_enabled"`
	PerFrameNoise       bool           `json:"per_frame_noise"`
	FECEnabled          bool           `json:"fec_enabled"`
	Directional         bool           `json:"directional"`
	Distribution        string         `json:"distribution"`
	ExplicitWeights     []MuxLegWeight `json:"explicit_weights,omitempty"`
	EcfMinWindowBytes   int64          `json:"ecf_min_window_bytes"`
	EcfMaxWindowBytes   int64          `json:"ecf_max_window_bytes"`
	EcfWindowMargin     float64        `json:"ecf_window_margin"`
	SendWindowWaitMax   string         `json:"send_window_wait_max"`
	ForwardSpill        bool           `json:"forward_spill"`
	ForwardSwitchMargin float64        `json:"forward_switch_margin"`
	PerLegWindowBytes   []float64      `json:"per_leg_window_bytes,omitempty"`
}

// MuxNegotiatedList is the printable list of them.
type MuxNegotiatedList struct {
	App    string          `json:"app"`
	Groups []MuxNegotiated `json:"groups"`
}

// Human prints one row per group: the caps that are on, then the window shape.
func (l MuxNegotiatedList) Human(w io.Writer) error {
	if len(l.Groups) == 0 {
		_, err := fmt.Fprintf(w, "no active route groups for app=%s\n", l.App)
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw,
		"RG\tLEGS\tDIST\tMUX\tSACK\tHOLRETX\tNOISE\tFEC\tDIR\tWIN_MIN\tWIN_MAX\tMARGIN\tWAIT_MAX"); err != nil {
		return err
	}
	for _, g := range l.Groups {
		if _, err := fmt.Fprintf(tw, "%d\t%d\t%s\t%v\t%v\t%v\t%v\t%v\t%v\t%d\t%d\t%.2f\t%s\n",
			g.DstPort, g.Legs, g.Distribution, g.MuxEnabled, g.SACKEnabled, g.HOLRetxEnabled,
			g.PerFrameNoise, g.FECEnabled, g.Directional,
			g.EcfMinWindowBytes, g.EcfMaxWindowBytes, g.EcfWindowMargin, g.SendWindowWaitMax); err != nil {
			return err
		}
	}
	return tw.Flush()
}
