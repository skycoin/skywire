package visorconfig

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestParseResolverSpec covers the grammar RESOLVERS entries are written in.
// The negative cases matter more than the positive one: a spec that parses
// into something OTHER than what the operator wrote produces a resolver that
// runs and misbehaves, which is strictly worse than one that refuses to start.
func TestParseResolverSpec(t *testing.T) {
	_, sk := cipher.GenerateKeyPair()

	t.Run("minimal", func(t *testing.T) {
		r, err := ParseResolverSpec("dmsg:4447")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if r.Kind != ResolverKindDmsg || r.ProxyPort != 4447 || !r.Enable {
			t.Fatalf("got %+v", r)
		}
		if !r.Chained() {
			t.Error("chain should default to on")
		}
	})

	t.Run("all options", func(t *testing.T) {
		spec := "dmsg:4447;name=lan;addr=0.0.0.0;suffix=.alt;sk=" + sk.Hex() +
			";upstream=127.0.0.1:1080;chain=false;alias=gw"
		r, err := ParseResolverSpec(spec)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if r.Name != "lan" || r.ProxyAddr != "0.0.0.0" || r.DomainSuffix != ".alt" ||
			r.UpstreamSOCKS != "127.0.0.1:1080" || r.Alias != "gw" {
			t.Fatalf("got %+v", r)
		}
		if r.SecretKey == nil || r.SecretKey.Hex() != sk.Hex() {
			t.Fatalf("secret key not parsed: %+v", r.SecretKey)
		}
		if r.Chained() {
			t.Error("chain=false should disable chaining")
		}
	})

	t.Run("suffix gains its leading dot", func(t *testing.T) {
		r, err := ParseResolverSpec("dmsg:4447;suffix=alt")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if r.DomainSuffix != ".alt" {
			t.Fatalf("got %q, want %q", r.DomainSuffix, ".alt")
		}
	})

	bad := []struct{ name, spec, want string }{
		{"no port", "dmsg", "want <kind>:<port>"},
		{"zero port", "dmsg:0", "invalid port"},
		{"bad kind", "quic:4447", "unknown kind"},
		// A typo'd option must NOT be shrugged off: "adrr=0.0.0.0" silently
		// ignored leaves the operator with a loopback-only resolver they
		// believe is serving the LAN.
		{"typo'd option", "dmsg:4447;adrr=0.0.0.0", "unknown option"},
		{"option without =", "dmsg:4447;addr", "not key=value"},
		{"sk on skynet", "skynet:4448;sk=" + sk.Hex(), "only meaningful for a dmsg resolver"},
		{"bad chain", "dmsg:4447;chain=maybe", "wants true/false"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ParseResolverSpec(c.spec); err == nil {
				t.Fatalf("want error containing %q, got nil", c.want)
			} else if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not contain %q", err, c.want)
			}
		})
	}
}

// TestParseResolverSpecs_OrderAndNames pins the stable naming: a regen of the
// same skywire.conf must produce a byte-identical config, so entries are
// port-ordered and the generated r0/r1 names follow that order rather than
// however the shell array happened to be written.
func TestParseResolverSpecs_OrderAndNames(t *testing.T) {
	rs, err := ParseResolverSpecs("skynet:4448, dmsg:4447")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rs) != 2 {
		t.Fatalf("want 2 entries, got %d", len(rs))
	}
	if rs[0].ProxyPort != 4447 || rs[0].Name != "r0" {
		t.Errorf("entry 0 = %+v", rs[0])
	}
	if rs[1].ProxyPort != 4448 || rs[1].Name != "r1" {
		t.Errorf("entry 1 = %+v", rs[1])
	}
}

func boolPtr(b bool) *bool { return &b }

func TestValidateResolvers(t *testing.T) {
	base := func(rs ...ResolverConfig) *V1 {
		return &V1{
			DmsgWeb:   &DmsgWebConfig{Enable: true},
			SkynetWeb: &SkynetWebConfig{Enable: true},
			Resolvers: rs,
		}
	}

	t.Run("primaries alone are fine", func(t *testing.T) {
		if err := base().ValidateResolvers(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// The whole point of the check: an extra resolver that lands on the
	// primary's default port, which the primary's own config never mentions.
	t.Run("conflict with an unset primary port", func(t *testing.T) {
		err := base(ResolverConfig{Name: "x", Enable: true, ProxyPort: DefaultDmsgWebProxyPort}).ValidateResolvers()
		if err == nil {
			t.Fatal("want a conflict error")
		}
		for _, want := range []string{"dmsg_web", "4445", "x"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not name %q", err, want)
			}
		}
	})

	t.Run("conflict between two extras", func(t *testing.T) {
		err := base(
			ResolverConfig{Name: "a", Enable: true, ProxyPort: 4447},
			ResolverConfig{Name: "b", Enable: true, ProxyPort: 4447},
		).ValidateResolvers()
		if err == nil || !strings.Contains(err.Error(), "4447") {
			t.Fatalf("want a 4447 conflict, got %v", err)
		}
	})

	// A wildcard bind already covers loopback: these are not two listeners,
	// they are one bind and one EADDRINUSE.
	t.Run("wildcard conflicts with loopback", func(t *testing.T) {
		v := base(ResolverConfig{Name: "a", Enable: true, ProxyPort: 4447, ProxyAddr: "0.0.0.0"},
			ResolverConfig{Name: "b", Enable: true, ProxyPort: 4447})
		if err := v.ValidateResolvers(); err == nil {
			t.Fatal("want a conflict between 0.0.0.0 and 127.0.0.1 on one port")
		}
	})

	t.Run("distinct addresses on one port do not conflict", func(t *testing.T) {
		v := base(ResolverConfig{Name: "a", Enable: true, ProxyPort: 4447, ProxyAddr: "192.168.1.2"},
			ResolverConfig{Name: "b", Enable: true, ProxyPort: 4447, ProxyAddr: "10.0.0.2"})
		if err := v.ValidateResolvers(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// A disabled entry binds nothing, so flagging it would fail configs that
	// work today.
	t.Run("disabled entries are not port-checked", func(t *testing.T) {
		v := base(ResolverConfig{Name: "a", ProxyPort: DefaultDmsgWebProxyPort})
		if err := v.ValidateResolvers(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("missing port", func(t *testing.T) {
		v := base(ResolverConfig{Name: "a", Enable: true})
		if err := v.ValidateResolvers(); err == nil || !strings.Contains(err.Error(), "proxy_port is required") {
			t.Fatalf("want a required-port error, got %v", err)
		}
	})

	t.Run("duplicate names", func(t *testing.T) {
		v := base(ResolverConfig{Name: "a", Enable: true, ProxyPort: 4447},
			ResolverConfig{Name: "a", Enable: true, ProxyPort: 4448})
		if err := v.ValidateResolvers(); err == nil || !strings.Contains(err.Error(), "duplicate resolver name") {
			t.Fatalf("want a duplicate-name error, got %v", err)
		}
	})

	t.Run("name must be app-safe", func(t *testing.T) {
		v := base(ResolverConfig{Name: "LAN gw", Enable: true, ProxyPort: 4447})
		if err := v.ValidateResolvers(); err == nil {
			t.Fatal("want a name-charset error")
		}
	})

	t.Run("route_timeout rejected on a dmsg entry", func(t *testing.T) {
		v := base(ResolverConfig{Name: "a", Enable: true, ProxyPort: 4447, RouteTimeout: Duration(1)})
		if err := v.ValidateResolvers(); err == nil || !strings.Contains(err.Error(), "route_timeout") {
			t.Fatalf("want a route_timeout error, got %v", err)
		}
	})

	t.Run("unnamed entries get names", func(t *testing.T) {
		v := base(ResolverConfig{Enable: true, ProxyPort: 4447})
		if err := v.ValidateResolvers(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v.Resolvers[0].Name != "r0" {
			t.Fatalf("name = %q, want r0", v.Resolvers[0].Name)
		}
		if got := v.Resolvers[0].AppName(); got != "resolver-r0" {
			t.Fatalf("app name = %q", got)
		}
	})
}

// TestResolversBackwardCompatible is the hard requirement: a skywire-config.json
// written before `resolvers` existed must load unchanged and re-marshal without
// growing a key.
func TestResolversBackwardCompatible(t *testing.T) {
	const old = `{
	  "version": "v1.3.0",
	  "sk": "0000000000000000000000000000000000000000000000000000000000000001",
	  "dmsg_web": {"enable": true, "proxy_port": 4445},
	  "skynet_web": {"enable": true},
	  "survey_whitelist": [],
	  "hypervisors": [],
	  "cli_addr": "localhost:3435",
	  "log_level": "info",
	  "local_path": "./local",
	  "stun_servers": [],
	  "is_public": false,
	  "geoip": "",
	  "persistent_transports": []
	}`
	var v V1
	v.Common = &Common{}
	if err := json.Unmarshal([]byte(old), &v); err != nil {
		t.Fatalf("unmarshal legacy config: %v", err)
	}
	if v.Resolvers != nil {
		t.Fatalf("legacy config grew resolvers: %+v", v.Resolvers)
	}
	if err := v.ValidateResolvers(); err != nil {
		t.Fatalf("legacy config rejected: %v", err)
	}
	out, err := json.Marshal(&v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), `"resolvers"`) {
		t.Fatalf("re-marshal added a resolvers key:\n%s", out)
	}
}

// TestResolverConfigRoundTrip pins that every knob survives a JSON round-trip.
// A resolver that loses its secret_key or its 0.0.0.0 bind on a reload turns a
// LAN gateway into a loopback proxy under the visor's own identity — silently.
func TestResolverConfigRoundTrip(t *testing.T) {
	_, sk := cipher.GenerateKeyPair()
	want := ResolverConfig{
		Name: "lan", Kind: ResolverKindDmsg, Enable: true, SecretKey: &sk,
		ProxyPort: 4447, ProxyAddr: "0.0.0.0", DomainSuffix: ".dmsg",
		UpstreamSOCKS: "127.0.0.1:1080", Chain: boolPtr(false), Alias: "gw",
		SelfLoopback: boolPtr(false), SelfLoopbackAuthenticated: boolPtr(false),
		TLSMITM: true, TLSPort: 443, TLSCAPath: "/ca.pem", TLSCAKeyPath: "/ca.key",
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got ResolverConfig
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SecretKey == nil || got.SecretKey.Hex() != sk.Hex() {
		t.Fatalf("secret key lost: %+v", got.SecretKey)
	}
	got.SecretKey, want.SecretKey = nil, nil
	if *got.Chain != false || *got.SelfLoopback != false || *got.SelfLoopbackAuthenticated != false {
		t.Fatalf("bool pointers lost: %+v", got)
	}
	got.Chain, want.Chain = nil, nil
	got.SelfLoopback, want.SelfLoopback = nil, nil
	got.SelfLoopbackAuthenticated, want.SelfLoopbackAuthenticated = nil, nil
	if got != want {
		t.Fatalf("round-trip changed the entry:\n got %+v\nwant %+v", got, want)
	}
}

// TestToDmsgWeb_KeepsAddrAndKey guards the projection the runtime actually
// consumes: these two fields are the ones whose loss would be invisible.
func TestToDmsgWeb_KeepsAddrAndKey(t *testing.T) {
	_, sk := cipher.GenerateKeyPair()
	r := ResolverConfig{Enable: true, ProxyPort: 4447, ProxyAddr: "0.0.0.0", SecretKey: &sk}
	d := r.ToDmsgWeb()
	if d.ProxyAddr != "0.0.0.0" || d.ProxyPort != 4447 || d.SecretKey == nil || !d.Enable {
		t.Fatalf("got %+v", d)
	}
	sr := ResolverConfig{Kind: ResolverKindSkynet, Enable: true, ProxyPort: 4448, RouteTimeout: Duration(5)}
	s := sr.ToSkynetWeb()
	if s.ProxyPort != 4448 || s.RouteTimeout != Duration(5) {
		t.Fatalf("got %+v", s)
	}
}

func TestResolverListenAddr(t *testing.T) {
	cases := []struct {
		addr string
		port uint
		want string
	}{
		{"", 4445, "127.0.0.1:4445"},
		{"0.0.0.0", 4445, "0.0.0.0:4445"},
		{"", 0, ""},
	}
	for _, c := range cases {
		if got := ResolverListenAddr(c.addr, c.port); got != c.want {
			t.Errorf("ResolverListenAddr(%q,%d) = %q, want %q", c.addr, c.port, got, c.want)
		}
	}
}
