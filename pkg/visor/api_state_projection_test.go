// Package visor pkg/visor/api_state_projection_test.go
package visor

import "testing"

// TestStateFieldSet_All: a nil/empty --select builds every DEFAULT section
// (proxy stays opt-in even for the full snapshot, so its heavy provider call is
// never made implicitly).
func TestStateFieldSet_All(t *testing.T) {
	for _, fields := range [][]string{nil, {}, {""}, {"", ""}} {
		set := newStateFieldSet(fields)
		if set != nil {
			t.Fatalf("newStateFieldSet(%q) = %v, want nil (full snapshot)", fields, set)
		}
		for _, k := range StateSelectKeys {
			want := k != SelectProxy // every default section, proxy excluded
			if got := set.has(k); got != want {
				t.Errorf("full snapshot: has(%q) = %v, want %v", k, got, want)
			}
		}
	}
}

// TestStateFieldSet_Projection: --select mux gates ONLY the mux section (this is
// the efficiency contract — transports/apps/etc are not built, so their ~307 KB
// work is skipped).
func TestStateFieldSet_Projection(t *testing.T) {
	set := newStateFieldSet([]string{SelectMux})
	if set == nil {
		t.Fatal("newStateFieldSet([mux]) = nil, want a set")
	}
	built := map[string]bool{}
	for _, k := range StateSelectKeys {
		built[k] = set.has(k)
	}
	if !built[SelectMux] {
		t.Error("--select mux must build mux")
	}
	for _, k := range []string{SelectSummary, SelectHealth, SelectRouting, SelectApps, SelectTransports, SelectModules, SelectCXO, SelectProxy} {
		if built[k] {
			t.Errorf("--select mux must NOT build %q", k)
		}
	}
}

// TestStateFieldSet_ProxyOptIn: proxy is built ONLY when named explicitly, never
// as a side effect of the full snapshot or another key.
func TestStateFieldSet_ProxyOptIn(t *testing.T) {
	if newStateFieldSet([]string{SelectMux}).has(SelectProxy) {
		t.Error("--select mux must not build proxy")
	}
	if !newStateFieldSet([]string{SelectProxy}).has(SelectProxy) {
		t.Error("--select proxy must build proxy")
	}
	if newStateFieldSet(nil).has(SelectProxy) {
		t.Error("full snapshot must not build proxy")
	}
}

// TestStateFieldSet_Multi: comma-selected multiple keys each gate on.
func TestStateFieldSet_Multi(t *testing.T) {
	set := newStateFieldSet([]string{SelectHealth, SelectRouting})
	if !set.has(SelectHealth) || !set.has(SelectRouting) {
		t.Error("multi-select must build both requested keys")
	}
	if set.has(SelectTransports) {
		t.Error("multi-select must not build an unrequested key")
	}
}
