// Package policy pkg/router/policy/distribution.go — parses the
// Starlark-emitted RouteSpec.Distribution descriptor into the
// router-domain DistributionConfig the route group's selector
// consumes. Keeps the parser in the policy package so pkg/router
// stays free of the grammar (it just sees a populated struct).
//
// Vocabulary (RFC #2882 phase 5):
//
//	""                          → DistributionUnset (no override)
//	"auto"                      → DistributionAuto (latency-weighted)
//	"round-robin" / "equal"     → DistributionRoundRobin
//	"weighted: f1, f2, ..., fN" → DistributionWeighted with N weights
//	"size-threshold: N"         → DistributionSizeThreshold with N bytes
//
// Whitespace is tolerant; case is not (descriptors are lowercase).
package policy

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/skycoin/skywire/pkg/router"
)

// ParseDistribution translates the script's descriptor into a
// router.DistributionConfig. Empty / "auto" / "round-robin" /
// "equal" map to the existing selector modes; "weighted:..." and
// "size-threshold:..." carry their config fields.
//
// Returns DistributionConfig{} (Mode=DistributionUnset) for the
// empty string so callers can branch on Mode and skip the
// override when no policy was set. Any other parse failure
// returns an error — the caller (the policy Hook) treats parse
// errors as "no override, log it" rather than failing the dial.
func ParseDistribution(s string) (router.DistributionConfig, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return router.DistributionConfig{Mode: router.DistributionUnset}, nil
	}

	switch s {
	case "auto":
		return router.DistributionConfig{Mode: router.DistributionAuto}, nil
	case "round-robin", "equal":
		return router.DistributionConfig{Mode: router.DistributionRoundRobin}, nil
	}

	// Prefix-based: "weighted: ..." and "size-threshold: ..."
	if name, body, ok := splitDescriptor(s); ok {
		switch name {
		case "weighted":
			return parseWeighted(body)
		case "size-threshold":
			return parseSizeThreshold(body)
		default:
			return router.DistributionConfig{}, fmt.Errorf("unknown distribution descriptor %q", name)
		}
	}
	return router.DistributionConfig{}, fmt.Errorf("malformed distribution descriptor %q", s)
}

// splitDescriptor pulls "name" and "body" out of "name: body".
// Accepts whitespace around the colon. Returns ok=false when
// there's no colon.
func splitDescriptor(s string) (name, body string, ok bool) {
	colon := strings.IndexByte(s, ':')
	if colon < 0 {
		return "", "", false
	}
	return strings.TrimSpace(s[:colon]), strings.TrimSpace(s[colon+1:]), true
}

// parseWeighted parses a comma-separated weight list.
//
//	"0.5, 0.3, 0.2" → [0.5, 0.3, 0.2]
//	"3, 1"          → [3, 1]
//
// Weights must be non-negative; at least one must be positive.
// Length matching against actual leg count is the route-side
// responsibility — the parser stays oblivious because the leg
// count isn't known at parse time.
func parseWeighted(body string) (router.DistributionConfig, error) {
	parts := strings.Split(body, ",")
	weights := make([]float64, 0, len(parts))
	anyPositive := false
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t == "" {
			continue
		}
		w, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return router.DistributionConfig{}, fmt.Errorf("weighted: parse %q: %w", t, err)
		}
		if w < 0 {
			return router.DistributionConfig{}, fmt.Errorf("weighted: weight %g is negative", w)
		}
		if w > 0 {
			anyPositive = true
		}
		weights = append(weights, w)
	}
	if len(weights) == 0 {
		return router.DistributionConfig{}, fmt.Errorf("weighted: empty weight list")
	}
	if !anyPositive {
		return router.DistributionConfig{}, fmt.Errorf("weighted: all weights are zero")
	}
	return router.DistributionConfig{
		Mode:    router.DistributionWeighted,
		Weights: weights,
	}, nil
}

// parseSizeThreshold parses the byte threshold. Must be positive
// — a zero threshold would push every packet to the wide-pipe
// leg, which is what the operator gets with mux=1 anyway.
func parseSizeThreshold(body string) (router.DistributionConfig, error) {
	t := strings.TrimSpace(body)
	n, err := strconv.Atoi(t)
	if err != nil {
		return router.DistributionConfig{}, fmt.Errorf("size-threshold: parse %q: %w", t, err)
	}
	if n <= 0 {
		return router.DistributionConfig{}, fmt.Errorf("size-threshold: %d is not positive", n)
	}
	return router.DistributionConfig{
		Mode:          router.DistributionSizeThreshold,
		SizeThreshold: n,
	}, nil
}
