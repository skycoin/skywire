package visor_test

// pkg/visor/dto_mirror_drift_test.go
//
// pkg/wasmhv (the browser wasm-HV core) cannot import pkg/visor — pkg/visor
// doesn't compile to js/wasm — so it hand-MIRRORS a handful of visor DTOs,
// keeping "gob field NAMES + json tags match pkg/visor.*" as a written contract
// (pkg/wasmhv/core.go). A silent drift (a field renamed or re-tagged on the
// native side without the mirror following) breaks the browser visor's HV
// surface: gob decodes of remote overviews misalign, or the Angular UI reads a
// key the wasm core no longer emits.
//
// This test makes that contract machine-enforced. It lives in an EXTERNAL test
// package (visor_test) so it may import BOTH pkg/visor and pkg/wasmhv (pkg/visor
// already depends on pkg/wasmhv, so there is no cycle). For every field the
// MIRROR declares, the native original must declare a field with the SAME Go
// name (gob identity) and the SAME json key (wire identity). The mirror is a
// deliberate SUBSET — the native may add fields the browser doesn't read — so
// the check is one-directional (mirror ⊆ native), which is exactly the drift
// that bites: it fires the moment a mirrored field diverges from its origin.
//
// Full physical unification of these DTOs into one shared js-safe package is
// tracked separately; this guard prevents silent drift until then.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/wasmhv"
)

// fieldJSONByName maps each exported struct field's Go name to its json key
// (the tag's name component, or the field name when untagged). json:"-" fields
// are skipped — they cross neither the gob nor the json wire.
func fieldJSONByName(t reflect.Type) map[string]string {
	out := make(map[string]string, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" { // unexported
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		key := strings.Split(tag, ",")[0]
		if key == "" {
			key = f.Name
		}
		out[f.Name] = key
	}
	return out
}

func assertMirrorSubset(t *testing.T, mirror, native reflect.Type) {
	t.Helper()
	nat := fieldJSONByName(native)
	for name, mkey := range fieldJSONByName(mirror) {
		nkey, ok := nat[name]
		if !ok {
			t.Errorf("%s mirrors %s, but field %q is absent from the native type — it was renamed or removed on the native side without updating the mirror (wasm HV surface drift)",
				mirror, native, name)
			continue
		}
		if nkey != mkey {
			t.Errorf("%s.%s json key %q != native %s.%s json key %q — the json tag drifted; the browser visor emits/reads a different wire key than the native visor",
				mirror, name, mkey, native, name, nkey)
		}
	}
}

// TestWasmhvMirrorsMatchVisorDTOs pins the pkg/wasmhv mirror structs to their
// pkg/visor originals. Add a pair here whenever pkg/wasmhv mirrors another
// visor DTO.
func TestWasmhvMirrorsMatchVisorDTOs(t *testing.T) {
	pairs := []struct {
		name           string
		mirror, native interface{}
	}{
		{"Overview", wasmhv.Overview{}, visor.Overview{}},
		{"Summary", wasmhv.Summary{}, visor.Summary{}},
		{"TransportSummary", wasmhv.TransportSummary{}, visor.TransportSummary{}},
		{"About", wasmhv.About{}, visor.About{}},
		{"HealthInfo", wasmhv.HealthInfo{}, visor.HealthInfo{}},
	}
	for _, p := range pairs {
		t.Run(p.name, func(t *testing.T) {
			assertMirrorSubset(t, reflect.TypeOf(p.mirror), reflect.TypeOf(p.native))
		})
	}
}
