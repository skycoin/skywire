// Package visorconfig pkg/visor/visorconfig/config_compat_mirror_test.go
// — holds the v1JSON unmarshal mirror to V1's JSON-tagged field set.
package visorconfig

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// jsonTags collects the json key of every tagged field on a struct
// type, skipping "-" and untagged fields (V1's mutex).
func jsonTags(t *testing.T, typ reflect.Type) map[string]bool {
	t.Helper()
	tags := make(map[string]bool)
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		if name, _, found := strings.Cut(tag, ","); found {
			tag = name
		}
		tags[tag] = true
	}
	return tags
}

// TestV1JSONMirrorsEveryTaggedField pins the invariant v1JSON's own
// comment claims: it mirrors V1's JSON-tagged field set verbatim.
//
// It did not. Wisp was added to V1 and never mirrored, so
// V1.UnmarshalJSON — which decodes into v1JSON and copies the fields
// back — dropped the `wisp` section from every config it loaded, on
// every platform. gen wrote the section correctly and every reader
// lost it, which made the embedded Wisp server impossible to enable:
// the visor saw a nil Wisp and skipped it. Nothing failed; the
// section simply evaporated between writing the file and reading it.
//
// A hand-maintained duplicate of a struct needs a test that fails when
// the two drift, which is what this is — the read-side counterpart of
// genvisor's marshal_compare_test.go.
func TestV1JSONMirrorsEveryTaggedField(t *testing.T) {
	want := jsonTags(t, reflect.TypeOf(V1{}))
	got := jsonTags(t, reflect.TypeOf(v1JSON{}))

	for tag := range want {
		if !got[tag] {
			t.Errorf("v1JSON is missing V1's %q field — it will be dropped on load", tag)
		}
	}
	for tag := range got {
		if !want[tag] && tag != "dmsgpty" {
			t.Errorf("v1JSON has %q, which V1 does not — it will never be written", tag)
		}
	}
}

// TestWispSurvivesUnmarshal is the concrete case that exposed the
// drift: a config file carrying a wisp section must populate V1.Wisp.
func TestWispSurvivesUnmarshal(t *testing.T) {
	const raw = `{"wisp": {"enable": true, "port": 6001}}`
	var v V1
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if v.Wisp == nil {
		t.Fatal("V1.Wisp was nil; the wisp section was dropped on load")
	}
	if !v.Wisp.Enable {
		t.Error("Wisp.Enable = false, want true")
	}
	if v.Wisp.Port != 6001 {
		t.Errorf("Wisp.Port = %d, want 6001", v.Wisp.Port)
	}
}
