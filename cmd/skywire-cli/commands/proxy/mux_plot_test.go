// Package skysocksc cmd/skywire-cli/commands/proxy/mux_plot_test.go c4-vis-cli
package skysocksc

import (
	"testing"
)

// TestParseMuxPlotOverrides pins the `--override key=value` parser that feeds
// the routing-policy CLIOverrides the conditional presets read (avoid_geo /
// trusted_pks / business_hours).
func TestParseMuxPlotOverrides(t *testing.T) {
	t.Run("nil for no flags leaves presets on defaults", func(t *testing.T) {
		m, err := parseMuxPlotOverrides(nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m != nil {
			t.Errorf("want nil map for no --override, got %v", m)
		}
	})

	t.Run("repeatable pairs parse into the map", func(t *testing.T) {
		m, err := parseMuxPlotOverrides([]string{"avoid_geo=RU,CN", "business_hours=9-17"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m["avoid_geo"] != "RU,CN" || m["business_hours"] != "9-17" {
			t.Errorf("parsed map wrong: %v", m)
		}
	})

	t.Run("value may contain '=' (only first splits)", func(t *testing.T) {
		m, err := parseMuxPlotOverrides([]string{"k=a=b"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m["k"] != "a=b" {
			t.Errorf("want value a=b, got %q", m["k"])
		}
	})

	t.Run("missing '=' or empty key rejected", func(t *testing.T) {
		for _, bad := range []string{"noequals", "=noKey"} {
			if _, err := parseMuxPlotOverrides([]string{bad}); err == nil {
				t.Errorf("expected error for %q, got nil", bad)
			}
		}
	})
}
