// Package visor pkg/visor/api_state_projection_test.go
package visor

import (
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"testing"
)

// TestStateFieldSet_All: a nil/empty --select builds every DEFAULT section
// (proxy stays opt-in even for the full snapshot, so its heavy provider call is
// never made implicitly).
func TestStateFieldSet_All(t *testing.T) {
	for _, fields := range [][]string{nil, {}, {""}, {"", ""}} {
		set := visorapi.NewStateFieldSet(fields)
		if set != nil {
			t.Fatalf("newStateFieldSet(%q) = %v, want nil (full snapshot)", fields, set)
		}
		for _, k := range visorapi.StateSelectKeys {
			want := k != visorapi.SelectProxy // every default section, proxy excluded
			if got := set.Has(k); got != want {
				t.Errorf("full snapshot: has(%q) = %v, want %v", k, got, want)
			}
		}
	}
}

// TestStateFieldSet_Projection: --select mux gates ONLY the mux section (this is
// the efficiency contract — transports/apps/etc are not built, so their ~307 KB
// work is skipped).
func TestStateFieldSet_Projection(t *testing.T) {
	set := visorapi.NewStateFieldSet([]string{visorapi.SelectMux})
	if set == nil {
		t.Fatal("newStateFieldSet([mux]) = nil, want a set")
	}
	built := map[string]bool{}
	for _, k := range visorapi.StateSelectKeys {
		built[k] = set.Has(k)
	}
	if !built[visorapi.SelectMux] {
		t.Error("--select mux must build mux")
	}
	for _, k := range []string{visorapi.SelectSummary, visorapi.SelectHealth, visorapi.SelectRouting, visorapi.SelectApps, visorapi.SelectTransports, visorapi.SelectModules, visorapi.SelectCXO, visorapi.SelectProxy} {
		if built[k] {
			t.Errorf("--select mux must NOT build %q", k)
		}
	}
}

// TestStateFieldSet_ProxyOptIn: proxy is built ONLY when named explicitly, never
// as a side effect of the full snapshot or another key.
func TestStateFieldSet_ProxyOptIn(t *testing.T) {
	if visorapi.NewStateFieldSet([]string{visorapi.SelectMux}).Has(visorapi.SelectProxy) {
		t.Error("--select mux must not build proxy")
	}
	if !visorapi.NewStateFieldSet([]string{visorapi.SelectProxy}).Has(visorapi.SelectProxy) {
		t.Error("--select proxy must build proxy")
	}
	if visorapi.NewStateFieldSet(nil).Has(visorapi.SelectProxy) {
		t.Error("full snapshot must not build proxy")
	}
}

// TestStateFieldSet_Multi: comma-selected multiple keys each gate on.
func TestStateFieldSet_Multi(t *testing.T) {
	set := visorapi.NewStateFieldSet([]string{visorapi.SelectHealth, visorapi.SelectRouting})
	if !set.Has(visorapi.SelectHealth) || !set.Has(visorapi.SelectRouting) {
		t.Error("multi-select must build both requested keys")
	}
	if set.Has(visorapi.SelectTransports) {
		t.Error("multi-select must not build an unrequested key")
	}
}

// TestStateFieldSet_JSONFieldNameAliases: a --select written with the
// snapshot's JSON field name resolves to the key that builds that section.
// `--select mux_route_groups` used to match nothing and return a snapshot with
// no mux_route_groups at all, which reads as "this visor has none".
func TestStateFieldSet_JSONFieldNameAliases(t *testing.T) {
	for field, want := range map[string]string{
		"mux_route_groups":      visorapi.SelectMux,
		"routing_stats":         visorapi.SelectRouting,
		"service_health":        visorapi.SelectHealth,
		"persistent_transports": visorapi.SelectTransports,
	} {
		set := visorapi.NewStateFieldSet([]string{field})
		if !set.Has(want) {
			t.Errorf("--select %q: has(%q) = false, want true", field, want)
		}
		if set.Has(visorapi.SelectApps) {
			t.Errorf("--select %q also built apps, want only %q", field, want)
		}
	}
}
