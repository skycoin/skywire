// Package policy pkg/router/policy/distribution_test.go — pins
// the descriptor grammar promised by RFC #2882 phase 5.
package policy

import (
	"testing"

	"github.com/skycoin/skywire/pkg/router"
)

func TestParseDistribution_EmptyAndAliases(t *testing.T) {
	cases := []struct {
		in       string
		wantMode router.DistributionMode
	}{
		{"", router.DistributionUnset},
		{"  ", router.DistributionUnset},
		{"auto", router.DistributionAuto},
		{"round-robin", router.DistributionRoundRobin},
		{"equal", router.DistributionRoundRobin},
	}
	for _, c := range cases {
		cfg, err := ParseDistribution(c.in)
		if err != nil {
			t.Errorf("ParseDistribution(%q): unexpected error %v", c.in, err)
			continue
		}
		if cfg.Mode != c.wantMode {
			t.Errorf("ParseDistribution(%q): Mode=%v, want %v", c.in, cfg.Mode, c.wantMode)
		}
	}
}

func TestParseDistribution_Weighted(t *testing.T) {
	cfg, err := ParseDistribution("weighted: 0.5, 0.3, 0.2")
	if err != nil {
		t.Fatalf("ParseDistribution: %v", err)
	}
	if cfg.Mode != router.DistributionWeighted {
		t.Errorf("Mode=%v, want DistributionWeighted", cfg.Mode)
	}
	if len(cfg.Weights) != 3 {
		t.Fatalf("Weights len=%d, want 3", len(cfg.Weights))
	}
	if cfg.Weights[0] != 0.5 || cfg.Weights[1] != 0.3 || cfg.Weights[2] != 0.2 {
		t.Errorf("Weights=%v, want [0.5 0.3 0.2]", cfg.Weights)
	}
}

func TestParseDistribution_WeightedIntegerForm(t *testing.T) {
	// Integer weights are also valid (parser doesn't care about
	// magnitude, just the relative ratio).
	cfg, err := ParseDistribution("weighted: 3, 1")
	if err != nil {
		t.Fatalf("ParseDistribution: %v", err)
	}
	if cfg.Mode != router.DistributionWeighted {
		t.Errorf("Mode=%v, want DistributionWeighted", cfg.Mode)
	}
	if len(cfg.Weights) != 2 || cfg.Weights[0] != 3 || cfg.Weights[1] != 1 {
		t.Errorf("Weights=%v, want [3 1]", cfg.Weights)
	}
}

func TestParseDistribution_WeightedErrors(t *testing.T) {
	cases := []string{
		"weighted:",
		"weighted: ,",
		"weighted: -1, 1",
		"weighted: 0, 0, 0",
		"weighted: abc",
	}
	for _, c := range cases {
		if _, err := ParseDistribution(c); err == nil {
			t.Errorf("ParseDistribution(%q): expected error, got nil", c)
		}
	}
}

func TestParseDistribution_SizeThreshold(t *testing.T) {
	cfg, err := ParseDistribution("size-threshold: 1400")
	if err != nil {
		t.Fatalf("ParseDistribution: %v", err)
	}
	if cfg.Mode != router.DistributionSizeThreshold {
		t.Errorf("Mode=%v, want DistributionSizeThreshold", cfg.Mode)
	}
	if cfg.SizeThreshold != 1400 {
		t.Errorf("SizeThreshold=%d, want 1400", cfg.SizeThreshold)
	}
}

func TestParseDistribution_SizeThresholdErrors(t *testing.T) {
	cases := []string{
		"size-threshold:",
		"size-threshold: 0",
		"size-threshold: -1",
		"size-threshold: foo",
	}
	for _, c := range cases {
		if _, err := ParseDistribution(c); err == nil {
			t.Errorf("ParseDistribution(%q): expected error, got nil", c)
		}
	}
}

func TestParseDistribution_UnknownDescriptor(t *testing.T) {
	if _, err := ParseDistribution("not-a-descriptor"); err == nil {
		t.Errorf("expected error for malformed descriptor, got nil")
	}
	if _, err := ParseDistribution("nonsense:foo"); err == nil {
		t.Errorf("expected error for unknown prefixed descriptor, got nil")
	}
	if _, err := ParseDistribution("sticky:not-a-qualifier"); err == nil {
		t.Errorf("expected error for unknown sticky qualifier, got nil")
	}
}

func TestParseDistribution_Sticky5Tuple(t *testing.T) {
	cfg, err := ParseDistribution("sticky: 5tuple")
	if err != nil {
		t.Fatalf("ParseDistribution: %v", err)
	}
	if cfg.Mode != router.DistributionSticky5Tuple {
		t.Errorf("Mode=%v, want DistributionSticky5Tuple", cfg.Mode)
	}
}

func TestParseDistribution_StickyErrors(t *testing.T) {
	cases := []string{
		"sticky:",
		"sticky: hash",
		"sticky: 5",
	}
	for _, c := range cases {
		if _, err := ParseDistribution(c); err == nil {
			t.Errorf("ParseDistribution(%q): expected error, got nil", c)
		}
	}
}

func TestParseDistribution_LatencyAdaptive(t *testing.T) {
	for _, s := range []string{"latency-adaptive", "latency-aware"} {
		cfg, err := ParseDistribution(s)
		if err != nil {
			t.Fatalf("ParseDistribution(%q): %v", s, err)
		}
		if cfg.Mode != router.DistributionLatencyAdaptive {
			t.Errorf("%q Mode=%v, want DistributionLatencyAdaptive", s, cfg.Mode)
		}
	}
}

func TestParseDistribution_DSCPPriority(t *testing.T) {
	cases := []struct {
		in        string
		threshold int
	}{
		{"dscp-priority: 46", 46},   // EF
		{"dscp-priority: 18", 18},   // AF21
		{"dscp-priority: 0x2e", 46}, // hex EF
		{"dscp-priority: 0x10", 16}, // hex AF11ish
	}
	for _, c := range cases {
		cfg, err := ParseDistribution(c.in)
		if err != nil {
			t.Errorf("ParseDistribution(%q): %v", c.in, err)
			continue
		}
		if cfg.Mode != router.DistributionDSCPPriority {
			t.Errorf("%q Mode=%v, want DistributionDSCPPriority", c.in, cfg.Mode)
		}
		if cfg.DSCPThreshold != c.threshold {
			t.Errorf("%q DSCPThreshold=%d, want %d", c.in, cfg.DSCPThreshold, c.threshold)
		}
	}
}

func TestParseDistribution_DSCPPriorityErrors(t *testing.T) {
	cases := []string{
		"dscp-priority:",
		"dscp-priority: 0",
		"dscp-priority: 64",
		"dscp-priority: -1",
		"dscp-priority: foo",
	}
	for _, c := range cases {
		if _, err := ParseDistribution(c); err == nil {
			t.Errorf("ParseDistribution(%q): expected error, got nil", c)
		}
	}
}
