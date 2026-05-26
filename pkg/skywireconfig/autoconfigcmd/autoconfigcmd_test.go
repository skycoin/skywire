package autoconfigcmd

import (
	"strings"
	"testing"
)

// TestNew_AllFlagsRegistered asserts every flag the pre-factory
// autoconfig command exposed is registered by New(). The list is
// hard-coded — adding a new flag means appending to both the
// factory and this test, which is the intent: the test guards
// against accidental flag drops, not against intentional adds.
func TestNew_AllFlagsRegistered(t *testing.T) {
	want := []string{
		"verbose",
		"hvpks",
		"ishv", "no-ishv",
		"rewardaddr",
		"public", "no-public",
		"stcpr", "sudph",
		"lan-dmsg-port", "lan-dmsg-public",
		"dmsgpty-pks",
		"vpnserver", "no-vpnserver",
		"proxyserver", "no-proxyserver",
		"skychat", "no-skychat",
		"dmsgweb", "no-dmsgweb",
		"skynetweb", "no-skynetweb",
		"disable-public-autoconn",
	}
	cmd := New(&Values{})
	for _, name := range want {
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
// gets garbage-collected after New() returns.
func TestNew_BindingsTrackValues(t *testing.T) {
	v := &Values{}
	cmd := New(v)
	if err := cmd.Flags().Set("hvpks", "0123,4567"); err != nil {
		t.Fatalf("Set hvpks: %v", err)
	}
	if v.Hvpks != "0123,4567" {
		t.Errorf("Values.Hvpks = %q after Set; want %q", v.Hvpks, "0123,4567")
	}
	if err := cmd.Flags().Set("ishv", "true"); err != nil {
		t.Fatalf("Set ishv: %v", err)
	}
	if !v.Ishv {
		t.Errorf("Values.Ishv = false after Set; want true")
	}
	if err := cmd.Flags().Set("stcpr", "7777"); err != nil {
		t.Fatalf("Set stcpr: %v", err)
	}
	if v.StcprPort != 7777 {
		t.Errorf("Values.StcprPort = %d after Set; want 7777", v.StcprPort)
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
	want := []string{
		"hvpks",
		"ishv", "no-ishv",
		"rewardaddr",
		"public", "no-public",
		"stcpr", "sudph",
		"lan-dmsg-port", "lan-dmsg-public",
		"dmsgpty-pks",
		"vpnserver", "no-vpnserver",
		"proxyserver", "no-proxyserver",
		"skychat", "no-skychat",
		"dmsgweb", "no-dmsgweb",
		"skynetweb", "no-skynetweb",
		"disable-public-autoconn",
	}
	for _, name := range want {
		if _, ok := m[name]; !ok {
			t.Errorf("envMap missing entry for --%s", name)
		}
	}
	// --verbose must NOT be in envMap (display-only flag).
	if _, ok := m["verbose"]; ok {
		t.Errorf("envMap should NOT have --verbose entry; that flag has no .conf effect")
	}
}

// TestEnvMap_NegationInversion asserts negation flags (--no-X)
// carry Negate=true so the literal CLI bool (always true when the
// flag is present) gets inverted to "false" at render time.
// Without this, --no-ishv would write ISHYPERVISOR=true — exactly
// the inverse of the operator's intent.
func TestEnvMap_NegationInversion(t *testing.T) {
	m := EnvMap()
	pairs := []struct {
		pos, neg string
	}{
		{"ishv", "no-ishv"},
		{"public", "no-public"},
		{"vpnserver", "no-vpnserver"},
		{"proxyserver", "no-proxyserver"},
		{"skychat", "no-skychat"},
		{"dmsgweb", "no-dmsgweb"},
		{"skynetweb", "no-skynetweb"},
	}
	for _, p := range pairs {
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
	// Sanity: every flag should appear in the help text.
	for _, name := range []string{"hvpks", "ishv", "rewardaddr"} {
		if !strings.Contains(usage, "--"+name) {
			t.Errorf("UsageString missing --%s", name)
		}
	}
}

