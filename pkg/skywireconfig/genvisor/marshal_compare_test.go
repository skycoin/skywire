package genvisor

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
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

// TestMustMarshalJSONNative_ResolverBlocks compares the hand-rolled serializer
// against encoding/json for a config whose resolver blocks are FULLY populated
// — the `resolvers` list plus every optional field of dmsg_web / skynet_web.
//
// The table-driven test above can't reach these: it feeds Generate() output,
// which leaves proxy_addr, secret_key, alias and self_loopback* unset, so a
// marshaler that dropped them matched the stdlib anyway. It did drop them, for
// exactly that reason. This case pins the fields themselves.
func TestMustMarshalJSONNative_ResolverBlocks(t *testing.T) {
	v, err := Generate(Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	_, sk := cipher.GenerateKeyPair()
	yes, no := true, false
	v.DmsgWeb = &visorconfig.DmsgWebConfig{
		Enable: true, SecretKey: &sk, ProxyPort: 4445, ProxyAddr: "0.0.0.0",
		WebPort: 8080, DomainSuffix: ".dmsg", UpstreamSOCKS: "127.0.0.1:4446",
		TLSMITM: true, TLSPort: 443, TLSCAPath: "/ca.pem", TLSCAKeyPath: "/ca.key",
		SelfLoopback: &no, Alias: "gw", SelfLoopbackAuthenticated: &yes,
		ForwardProxy: true, ForwardPort: 84,
	}
	v.SkynetWeb = &visorconfig.SkynetWebConfig{
		Enable: true, ProxyPort: 4446, ProxyAddr: "192.168.1.2", WebPort: 8081,
		DomainSuffix: ".skynet", UpstreamSOCKS: "127.0.0.1:1080",
		RouteTimeout: visorconfig.Duration(time.Hour),
		TLSMITM:      true, TLSPort: 443, TLSCAPath: "/ca.pem", TLSCAKeyPath: "/ca.key",
		SelfLoopback: &no, Alias: "gw", SelfLoopbackAuthenticated: &yes,
	}
	v.Wisp = &visorconfig.WispConfig{
		Enable: true, Port: 6001, UpstreamSOCKS: "127.0.0.1:1080", Buffer: 128,
	}
	v.Resolvers = []visorconfig.ResolverConfig{
		{
			Name: "lan", Kind: visorconfig.ResolverKindDmsg, Enable: true, SecretKey: &sk,
			ProxyPort: 4447, ProxyAddr: "0.0.0.0", DomainSuffix: ".dmsg",
			UpstreamSOCKS: "127.0.0.1:1080", Chain: &no, Alias: "lan",
			SelfLoopback: &no, SelfLoopbackAuthenticated: &no,
			TLSMITM: true, TLSPort: 8443, TLSCAPath: "/ca.pem", TLSCAKeyPath: "/ca.key",
		},
		{
			Name: "alt", Kind: visorconfig.ResolverKindSkynet, Enable: false,
			ProxyPort: 4448, RouteTimeout: visorconfig.Duration(time.Minute),
		},
	}

	want, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("stdlib MarshalIndent: %v", err)
	}
	got := MustMarshalJSONNative(v)
	var wantTree, gotTree interface{}
	if err := json.Unmarshal(want, &wantTree); err != nil {
		t.Fatalf("stdlib output not valid JSON: %v\n%s", err, want)
	}
	if err := json.Unmarshal(got, &gotTree); err != nil {
		t.Fatalf("hand-rolled output not valid JSON: %v\n%s", err, got)
	}
	wantCanon, _ := json.Marshal(wantTree) //nolint:errcheck
	gotCanon, _ := json.Marshal(gotTree)   //nolint:errcheck
	if string(wantCanon) != string(gotCanon) {
		t.Errorf("JSON structural mismatch\nstdlib: %s\nhand:   %s", wantCanon, gotCanon)
	}
}
