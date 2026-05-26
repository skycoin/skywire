package genvisor

import (
	"encoding/json"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestMustMarshalJSONNative_MatchesStdlib compares the hand-rolled
// streaming serializer (MustMarshalJSONNative — generated from
// marshal_js.go with the build tag stripped and symbol prefix
// added) against json.MarshalIndent over Generate output. The
// hand-rolled marshaler is what TinyGo WASM ships as
// genvisor.MustMarshalJSON; this comparison catches drift between
// the two implementations.
func TestMustMarshalJSONNative_MatchesStdlib(t *testing.T) {
	cases := []struct {
		name string
		opts Options
	}{
		{"defaults", Options{}},
		{"hypervisor", Options{IsHypervisor: true}},
		{"reward+public", Options{
			RewardAddress: "2jnZqqJsCMB1v4VUKk6f1PDN9EuLZQxqoqp",
			IsPublic:      true,
		}},
		{"pinned-sk", func() Options {
			_, sk := cipher.GenerateKeyPair()
			return Options{SecretKey: sk}
		}()},
		{"testenv", Options{TestEnv: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, err := Generate(c.opts)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}

			want, err := json.MarshalIndent(v, "", "  ")
			if err != nil {
				t.Fatalf("stdlib MarshalIndent: %v", err)
			}
			got := MustMarshalJSONNative(v)

			// Parse both as generic JSON and compare structurally.
			// Byte-for-byte equality is too strict — our hand-rolled
			// emitter may order map keys differently for the
			// STCP.pk_table map, and omitempty edge cases on the
			// stdlib side aren't perfectly reproducible without
			// reflect. Structural equality on the parsed shape is
			// what actually matters: the visor's loader uses
			// json.Unmarshal which is permissive on formatting.
			var wantTree, gotTree interface{}
			if err := json.Unmarshal(want, &wantTree); err != nil {
				t.Fatalf("stdlib output not valid JSON: %v\n%s", err, want)
			}
			if err := json.Unmarshal(got, &gotTree); err != nil {
				t.Fatalf("hand-rolled output not valid JSON: %v\n%s", err, got)
			}

			// Compare via canonical (sorted-keys) JSON form. The
			// re-marshal of an already-parsed map[string]interface{}
			// can't fail under any realistic conditions, so the
			// error returns are intentionally swallowed.
			wantCanon, err := json.Marshal(wantTree)
			if err != nil {
				t.Fatalf("canonical re-marshal of stdlib output: %v", err)
			}
			gotCanon, err := json.Marshal(gotTree)
			if err != nil {
				t.Fatalf("canonical re-marshal of hand-rolled output: %v", err)
			}
			if string(wantCanon) != string(gotCanon) {
				t.Errorf("JSON structural mismatch\nstdlib: %s\nhand:   %s", wantCanon, gotCanon)
			}
		})
	}
}
