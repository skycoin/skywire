// Package routersettings pkg/router/routersettings/shape.go c2-net-routing
//
// The mux.shape value grammar: auto | <k>x<n> | n1,n2,… — k tunnels holding
// n_1..n_k legs (docs/design/mux-shape-axis.md). It lives beside the knob
// rather than in pkg/router because the catalog validates a value before
// pkg/router ever sees it; pkg/router/mux_shape.go wraps what ParseShapeSpec
// returns in its Shape type, so the grammar has exactly one definition.
package routersettings

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ShapeAuto is the mux.shape value that leaves the shape to the router — the
// default, and today's behavior restated: as many tunnels as the app holds,
// each at pool.active_width legs.
const ShapeAuto = "auto"

// ValidateShape is the mux.shape knob's validator: ShapeAuto, or an explicit
// shape ParseShapeSpec accepts.
func ValidateShape(raw string) error {
	if strings.EqualFold(strings.TrimSpace(raw), ShapeAuto) {
		return nil
	}
	_, err := ParseShapeSpec(raw)
	return err
}

// ParseShapeSpec reads an EXPLICIT shape — "<k>x<n>", or a comma list giving
// each tunnel its own leg count — into the per-tunnel leg counts it names.
// Every count must be at least one: a tunnel with no leg is not a tunnel.
// ShapeAuto is not an explicit shape and is refused here.
func ParseShapeSpec(raw string) ([]int, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return nil, errors.New(`empty shape; want "auto", "<k>x<n>" or a comma list of leg counts`)
	}
	if s == ShapeAuto {
		return nil, fmt.Errorf("%q is not an explicit shape", ShapeAuto)
	}
	if kRaw, nRaw, ok := strings.Cut(s, "x"); ok {
		k, err := shapeInt(kRaw)
		if err != nil {
			return nil, fmt.Errorf("tunnel count: %w", err)
		}
		n, err := shapeInt(nRaw)
		if err != nil {
			return nil, fmt.Errorf("legs per tunnel: %w", err)
		}
		legs := make([]int, k)
		for i := range legs {
			legs[i] = n
		}
		return legs, nil
	}
	parts := strings.Split(s, ",")
	legs := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := shapeInt(p)
		if err != nil {
			return nil, fmt.Errorf("legs per tunnel: %w", err)
		}
		legs = append(legs, n)
	}
	return legs, nil
}

// FormatShapeSpec renders per-tunnel leg counts the way ParseShapeSpec takes
// them back: "<k>x<n>" when every tunnel is the same width, the comma list
// otherwise. The empty shape (no active tunnel) renders empty.
func FormatShapeSpec(legs []int) string {
	if len(legs) == 0 {
		return ""
	}
	uniform := true
	for _, n := range legs[1:] {
		if n != legs[0] {
			uniform = false
			break
		}
	}
	if uniform {
		return strconv.Itoa(len(legs)) + "x" + strconv.Itoa(legs[0])
	}
	parts := make([]string, 0, len(legs))
	for _, n := range legs {
		parts = append(parts, strconv.Itoa(n))
	}
	return strings.Join(parts, ",")
}

func shapeInt(raw string) (int, error) {
	s := strings.TrimSpace(raw)
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("want a positive integer, got %q", s)
	}
	return n, nil
}
