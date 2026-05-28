package autoconfigcmd

import (
	"strings"
	"testing"
)

// allFlags is the canonical list of every flag the factory exposes.
// Centralized so the registration + envMap + negation tests all
// agree on the same source of truth. Adding a new flag means adding
// it here AND to the factory; CI catches drift between the two.
var allFlags = []string{
	"verbose",
	// Hypervisor / identity
	"hvpks", "ishv", "no-ishv", "pk-endpoint", "no-pk-endpoint", "hvaddr", "sk", "version",
	// Visor public/private + autoconnect
	"rewardaddr", "public", "no-public", "publicip", "disable-public-autoconn",
	// Service discovery / deployment
	"testenv", "dmsghttp", "dmsgconf", "url", "svcconf", "minsess", "stun",
	// Transport ports
	"stcpr", "sudph", "lan-dmsg-port", "lan-dmsg-public",
	// Whitelists
	"dmsgpty-pks", "survey", "routesetup", "tpsetup",
	// Route calculation
	"calculate-routes",
	// VPN server
	"vpnserver", "no-vpnserver", "killsw", "addvpn", "vpnwl", "secure", "netifc",
	// Proxy
	"proxyserver", "no-proxyserver", "proxyclientpk", "startproxyclient", "proxywl",
	// SOCKS5 web bridges
	"dmsgweb", "no-dmsgweb", "skynetweb", "no-skynetweb", "dmsgweb-upstream", "skynetweb-upstream",
	// Skychat
	"skychat", "no-skychat", "chataddr", "servechatpair", "no-servechatpair",
	// Skymail bridge
	"skymail-bridge", "no-skymail-bridge",
	// Skycoin daemon
	"skycoind", "no-skycoind", "skycoindfiber", "skycoindapi", "skycoindUSER", "skycoindinstances", "skycoindflags",
	// Skycoin web wallet
	"skycoinweb", "no-skycoinweb", "skycoinwebaddr", "skycoinwebnodes", "skycoinwebwallet", "skycoinwebuser",
	// Visor runtime
	"binpath", "loglvl", "timeout", "regtimeout",
}

// negationPairs enumerates every positive/negation flag pair. The
// negation flag's envMap entry must have Negate=true and share the
// same Key. allBoolPairFlags is derived from this for the
// TestEnvMap_Coverage check.
var negationPairs = []struct {
	pos, neg string
}{
	{"ishv", "no-ishv"},
	{"pk-endpoint", "no-pk-endpoint"},
	{"public", "no-public"},
	{"vpnserver", "no-vpnserver"},
	{"proxyserver", "no-proxyserver"},
	{"skychat", "no-skychat"},
	{"dmsgweb", "no-dmsgweb"},
	{"skynetweb", "no-skynetweb"},
	{"servechatpair", "no-servechatpair"},
	{"skymail-bridge", "no-skymail-bridge"},
	{"skycoind", "no-skycoind"},
	{"skycoinweb", "no-skycoinweb"},
}

// TestNew_AllFlagsRegistered asserts every flag in allFlags is
// registered by New(). Adding a new flag means appending to the
// allFlags list AND to the factory; this test catches drift.
func TestNew_AllFlagsRegistered(t *testing.T) {
	cmd := New(&Values{})
	for _, name := range allFlags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("missing flag --%s", name)
		}
	}
}

// TestNew_ShortNames asserts the -v short flag for --verbose is
// preserved. Operators rely on the short form in scripts; renaming
// or dropping the short would be a backward-incompat surface break.
func TestNew_ShortNames(t *testing.T) {
	cmd := New(&Values{})
	verbose := cmd.Flags().Lookup("verbose")
	if verbose == nil {
		t.Fatal("verbose flag missing")
	}
	if verbose.Shorthand != "v" {
		t.Errorf("verbose shorthand = %q; want %q", verbose.Shorthand, "v")
	}
}

// TestNew_BindingsTrackValues asserts the returned command's flag
// bindings actually write into the Values struct fields. Catches
// the class of bug where the factory wires a flag to a local that
// gets garbage-collected after New() returns. A few representative
// flags from different value-types cover the typical breakage modes
// (string-bool-int / pre-and-post-expansion field positions).
func TestNew_BindingsTrackValues(t *testing.T) {
	v := &Values{}
	cmd := New(v)
	cases := []struct {
		flag, val string
		check     func() bool
	}{
		{"hvpks", "0123,4567", func() bool { return v.Hvpks == "0123,4567" }},
		{"ishv", "true", func() bool { return v.Ishv }},
		{"pk-endpoint", "true", func() bool { return v.PkEndpoint }},
		{"no-pk-endpoint", "true", func() bool { return v.NoPkEndpoint }},
		{"stcpr", "7777", func() bool { return v.StcprPort == 7777 }},
		// New flags from the parity expansion: cover one per group.
		{"testenv", "true", func() bool { return v.TestEnv }},
		{"minsess", "5", func() bool { return v.MinSess == 5 }},
		{"survey", "abc,def", func() bool { return v.SurveyPks == "abc,def" }},
		{"killsw", "/etc/foo", func() bool { return v.VpnKillSw == "/etc/foo" }},
		{"skycoindinstances", "a,b", func() bool { return v.SkycoindInstances == "a,b" }},
		{"loglvl", "debug", func() bool { return v.LogLevel == "debug" }},
		{"sk", "01" + strings.Repeat("0", 62), func() bool { return v.SecretKey == "01"+strings.Repeat("0", 62) }},
	}
	for _, c := range cases {
		if err := cmd.Flags().Set(c.flag, c.val); err != nil {
			t.Fatalf("Set %s=%q: %v", c.flag, c.val, err)
		}
		if !c.check() {
			t.Errorf("Values field for --%s did not pick up Set value %q", c.flag, c.val)
		}
	}
}

// TestEnvMap_Coverage asserts every operator-effective flag has an
// env-var entry, and that the only flag with NO env mapping is
// --verbose. Catches the bug where a new flag is added to the
// factory but its envMap entry is forgotten — the operator passes
// the flag, autoconfig silently no-ops because envMap doesn't know
// about it.
func TestEnvMap_Coverage(t *testing.T) {
	m := EnvMap()
	for _, name := range allFlags {
		if name == "verbose" {
			continue
		}
		if _, ok := m[name]; !ok {
			t.Errorf("envMap missing entry for --%s", name)
		}
	}
	// --verbose must NOT be in envMap (display-only flag).
	if _, ok := m["verbose"]; ok {
		t.Errorf("envMap should NOT have --verbose entry; that flag has no .conf effect")
	}
	// And every envMap key should be a registered flag.
	cmd := New(&Values{})
	for k := range m {
		if cmd.Flags().Lookup(k) == nil {
			t.Errorf("envMap has --%s but factory does not register it", k)
		}
	}
}

// TestEnvMap_NegationInversion asserts negation flags (--no-X)
// carry Negate=true so the literal CLI bool (always true when the
// flag is present) gets inverted to "false" at render time.
// Without this, --no-ishv would write ISHYPERVISOR=true — exactly
// the inverse of the operator's intent.
func TestEnvMap_NegationInversion(t *testing.T) {
	m := EnvMap()
	for _, p := range negationPairs {
		pos, ok := m[p.pos]
		if !ok {
			t.Errorf("envMap missing positive flag --%s", p.pos)
			continue
		}
		neg, ok := m[p.neg]
		if !ok {
			t.Errorf("envMap missing negation flag --%s", p.neg)
			continue
		}
		if pos.Negate {
			t.Errorf("positive flag --%s should have Negate=false; got true", p.pos)
		}
		if !neg.Negate {
			t.Errorf("negation flag --%s should have Negate=true; got false", p.neg)
		}
		if pos.Key != neg.Key {
			t.Errorf("--%s and --%s should share Key; got %q vs %q", p.pos, p.neg, pos.Key, neg.Key)
		}
	}
}

// TestEnvMap_UniqueKeys asserts the SKYENV variable names are
// unique across positive flags — two distinct positive flags
// pointing at the same SKYENV key would silently shadow each
// other when both are passed. Negation flags share keys with
// their positive partners by design and are excluded here.
func TestEnvMap_UniqueKeys(t *testing.T) {
	m := EnvMap()
	seen := map[string]string{}
	for flag, mapping := range m {
		if mapping.Negate {
			continue
		}
		if other, dup := seen[mapping.Key]; dup {
			t.Errorf("SKYENV key %q is written by both --%s and --%s (positive flags must be unique)", mapping.Key, other, flag)
		}
		seen[mapping.Key] = flag
	}
}

// TestNew_UsageString_RendersWithoutError asserts cobra's help
// rendering doesn't panic and produces a non-empty string. The
// WASM consumer (apt-repo install page) relies on this to render
// the operator-facing help in the browser.
func TestNew_UsageString_RendersWithoutError(t *testing.T) {
	cmd := New(&Values{})
	usage := cmd.UsageString()
	if usage == "" {
		t.Fatal("UsageString returned empty")
	}
	// Sanity: every registered flag should appear in the help text.
	// (We sample a few to keep the failure message readable.)
	for _, name := range []string{"hvpks", "ishv", "rewardaddr", "testenv", "skycoindinstances", "loglvl"} {
		if !strings.Contains(usage, "--"+name) {
			t.Errorf("UsageString missing --%s", name)
		}
	}
}
