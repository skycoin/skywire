// Package clisd cmd/skywire-cli/commands/sd/transport_counts.go c4-vis-cli
package clisd

import (
	"fmt"
	"strings"

	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// transportCounts is one key's transports by type: a field per known type
// (tptypes.Known), anything unrecognized in Other, and the Total of all.
type transportCounts struct {
	STCPR  int `json:"stcpr"`
	SQUICR int `json:"squicr"`
	SUDPH  int `json:"sudph"`
	STCP   int `json:"stcp"`
	WEBRTC int `json:"webrtc"`
	SWSR   int `json:"swsr"`
	SWTR   int `json:"swtr"`
	DMSG   int `json:"dmsg"`
	Other  int `json:"other,omitempty"`
	Total  int `json:"total"`
}

// field returns the counter for t, nil for an unrecognized type.
func (c *transportCounts) field(t tptypes.Type) *int {
	switch tptypes.NormalizeType(t) {
	case tptypes.STCPR:
		return &c.STCPR
	case tptypes.QUIC:
		return &c.SQUICR
	case tptypes.SUDPH:
		return &c.SUDPH
	case tptypes.STCP:
		return &c.STCP
	case tptypes.WEBRTC:
		return &c.WEBRTC
	case tptypes.WS:
		return &c.SWSR
	case tptypes.WT:
		return &c.SWTR
	case tptypes.DMSG:
		return &c.DMSG
	}
	return nil
}

// add counts one transport of type t, reporting whether t was recognized.
func (c *transportCounts) add(t tptypes.Type) bool {
	c.Total++
	if f := c.field(t); f != nil {
		*f++
		return true
	}
	c.Other++
	return false
}

// direct is the number of direct (non-relay) transports.
func (c *transportCounts) direct() int {
	n := 0
	for _, t := range tptypes.Known() {
		if tptypes.IsDirect(t) {
			n += *c.field(t)
		}
	}
	return n
}

// transportColumns is the tab-separated header for the per-type columns,
// with an "other" column when any unrecognized type was seen.
func transportColumns(other bool) string {
	cols := make([]string, 0, len(tptypes.Known())+1)
	for _, t := range tptypes.Known() {
		cols = append(cols, string(t))
	}
	if other {
		cols = append(cols, "other")
	}
	return strings.Join(cols, "\t")
}

// row is the tab-separated per-type counts, in transportColumns order.
func (c *transportCounts) row(other bool) string {
	vals := make([]string, 0, len(tptypes.Known())+1)
	for _, t := range tptypes.Known() {
		vals = append(vals, fmt.Sprint(*c.field(t)))
	}
	if other {
		vals = append(vals, fmt.Sprint(c.Other))
	}
	return strings.Join(vals, "\t")
}
