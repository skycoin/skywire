// Package cliconfig — applyFlagsToConf unit tests
//
// Covers the three flag categories applyFlagsToConf handles:
//
//  1. Boolean flags — uncomment lines like `#FLAG=true`.
//  2. Single-value string flags — uncomment + substitute lines like
//     `#KEY='oldval'` → `KEY='newval'`.
//  3. Bash-array flags (the regression this test guards against) —
//     uncomment + substitute lines of the empty-array template form
//     into KEY=('a' 'b' 'c'). Pre-fix, only categories 1 and 2 were
//     handled; an operator running `skywire cli config gen -qpij PK`
//     got the still-commented HYPERVISORPKS line even though -j was
//     set, defeating the point of -j.
package cliconfig

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// makeApplyFlagsCmd builds a minimal *cobra.Command with the same
// flag bindings applyFlagsToConf inspects. We don't reuse the real
// genConfigCmd because its PreRun has side effects (filepath resolution,
// log setup, etc.) we don't want in a unit test.
func makeApplyFlagsCmd() *cobra.Command {
	c := &cobra.Command{Use: "fake"}
	// Booleans
	for _, name := range []string{"pkg", "ishv", "testenv", "dmsghttp", "public", "servevpn", "autoconn", "publicip"} {
		c.Flags().Bool(name, false, "")
	}
	// Single-value strings
	for _, name := range []string{"cliaddr", "hvaddr", "timeout", "reward"} {
		c.Flags().String(name, "", "")
	}
	// Array-shaped (comma-separated)
	for _, name := range []string{"hvpks", "dmsgpty", "survey", "url", "tpsetup", "routesetup", "vpnwl", "proxywl", "skycoinwebnodes", "stun"} {
		c.Flags().String(name, "", "")
	}
	return c
}

func TestApplyFlagsToConf_ArrayFlag_HypervisorPKs_SinglePK(t *testing.T) {
	// The bug case the operator reported: `-qpij PK` should
	// uncomment HYPERVISORPKS in the rendered template.
	cmd := makeApplyFlagsCmd()
	if err := cmd.Flags().Set("hvpks", "0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c"); err != nil {
		t.Fatalf("flag set: %v", err)
	}

	in := "#--	Set remote hypervisor public keys\n#HYPERVISORPKS=('')\n"
	got := applyFlagsToConf(in, cmd)

	want := "HYPERVISORPKS=('0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c')"
	if !strings.Contains(got, want) {
		t.Fatalf("expected to contain %q; got:\n%s", want, got)
	}
	if strings.Contains(got, "#HYPERVISORPKS=") {
		t.Fatalf("expected the original commented line to be replaced; got:\n%s", got)
	}
}

func TestApplyFlagsToConf_ArrayFlag_HypervisorPKs_MultiplePKs(t *testing.T) {
	cmd := makeApplyFlagsCmd()
	if err := cmd.Flags().Set("hvpks", "AAA,BBB,CCC"); err != nil {
		t.Fatalf("flag set: %v", err)
	}
	in := "#HYPERVISORPKS=('')\n"
	got := applyFlagsToConf(in, cmd)
	want := "HYPERVISORPKS=('AAA' 'BBB' 'CCC')"
	if !strings.Contains(got, want) {
		t.Fatalf("expected %q; got %q", want, got)
	}
}

func TestApplyFlagsToConf_ArrayFlag_TrimsAndDropsEmptyEntries(t *testing.T) {
	// User typed extra commas or whitespace — should be normalized,
	// not produce empty array entries.
	cmd := makeApplyFlagsCmd()
	if err := cmd.Flags().Set("survey", " AAA , , BBB , "); err != nil {
		t.Fatalf("flag set: %v", err)
	}
	in := "#SURVEYPKS=('')\n"
	got := applyFlagsToConf(in, cmd)
	want := "SURVEYPKS=('AAA' 'BBB')"
	if !strings.Contains(got, want) {
		t.Fatalf("expected %q; got %q", want, got)
	}
}

func TestApplyFlagsToConf_ArrayFlag_EmptyValueIsNoOp(t *testing.T) {
	// Setting to an empty string (or all-whitespace) shouldn't touch
	// the template — the operator explicitly asked for "no keys".
	// Leaving the line commented preserves the canonical default.
	cmd := makeApplyFlagsCmd()
	if err := cmd.Flags().Set("dmsgpty", ""); err != nil {
		t.Fatalf("flag set: %v", err)
	}
	in := "#DMSGPTYPKS=('')\n"
	got := applyFlagsToConf(in, cmd)
	if !strings.Contains(got, "#DMSGPTYPKS=('')") {
		t.Fatalf("expected commented template to be preserved; got:\n%s", got)
	}
}

func TestApplyFlagsToConf_ArrayFlag_UnsetFlagIsNoOp(t *testing.T) {
	// Not calling Flags().Set at all = flag wasn't explicitly set →
	// must NOT modify the template line. Guards against the
	// `if val != ""` short-circuit being the only check (which would
	// trip on flags carrying a default value from scriptExecArray).
	cmd := makeApplyFlagsCmd()
	in := "#HYPERVISORPKS=('')\n#DMSGPTYPKS=('')\n"
	got := applyFlagsToConf(in, cmd)
	if got != in {
		t.Fatalf("expected no changes when no array flags set; got:\n%s", got)
	}
}

func TestApplyFlagsToConf_ArrayFlag_AllTenFlagsCovered(t *testing.T) {
	// Sanity check that every array env var present in the canonical
	// template responds to its flag. If someone adds a new
	// `#FOO=('')` to envfiles.go but forgets the matching entry in
	// arrayFlagToEnv, this test won't catch it — but it WILL catch
	// the inverse (entry added to the map but no template line),
	// which is the more common refactor mistake.
	cases := []struct {
		flag, envKey string
	}{
		{"hvpks", "HYPERVISORPKS"},
		{"dmsgpty", "DMSGPTYPKS"},
		{"survey", "SURVEYPKS"},
		{"url", "SVCCONFADDR"},
		{"tpsetup", "TPSETUPPKS"},
		{"routesetup", "ROUTESETUPPKS"},
		{"vpnwl", "VPNSERVERWL"},
		{"proxywl", "PROXYSERVERWL"},
		{"skycoinwebnodes", "SKYCOINWEBNODES"},
		{"stun", "STUNSERVERS"},
	}
	for _, c := range cases {
		t.Run(c.flag, func(t *testing.T) {
			cmd := makeApplyFlagsCmd()
			if err := cmd.Flags().Set(c.flag, "X,Y"); err != nil {
				t.Fatalf("flag set: %v", err)
			}
			in := "#" + c.envKey + "=('')\n"
			got := applyFlagsToConf(in, cmd)
			want := c.envKey + "=('X' 'Y')"
			if !strings.Contains(got, want) {
				t.Fatalf("expected %q; got %q", want, got)
			}
		})
	}
}

func TestApplyFlagsToConf_BooleanFlag_StillWorks(t *testing.T) {
	// Regression guard: the array-flag handler runs alongside the
	// existing boolean handler in the same line loop. Confirm the
	// boolean path still uncomments correctly.
	cmd := makeApplyFlagsCmd()
	if err := cmd.Flags().Set("pkg", "true"); err != nil {
		t.Fatalf("flag set: %v", err)
	}
	in := "#PKGENV=true\n"
	got := applyFlagsToConf(in, cmd)
	if !strings.Contains(got, "\nPKGENV=true") && !strings.HasPrefix(got, "PKGENV=true") {
		t.Fatalf("expected PKGENV=true uncommented; got %q", got)
	}
}

func TestApplyFlagsToConf_ValueFlag_StillWorks(t *testing.T) {
	cmd := makeApplyFlagsCmd()
	if err := cmd.Flags().Set("cliaddr", "0.0.0.0:3435"); err != nil {
		t.Fatalf("flag set: %v", err)
	}
	in := "#CLIADDR=''\n"
	got := applyFlagsToConf(in, cmd)
	want := "CLIADDR='0.0.0.0:3435'"
	if !strings.Contains(got, want) {
		t.Fatalf("expected %q; got %q", want, got)
	}
}

func TestApplyFlagsToConf_MixedFlags(t *testing.T) {
	// Operator's actual repro: -qpij PK ⇒ -p (pkg, bool),
	// -i (ishv, bool), -j PK (hvpks, array).
	cmd := makeApplyFlagsCmd()
	if err := cmd.Flags().Set("pkg", "true"); err != nil {
		t.Fatalf("flag set: %v", err)
	}
	if err := cmd.Flags().Set("ishv", "true"); err != nil {
		t.Fatalf("flag set: %v", err)
	}
	if err := cmd.Flags().Set("hvpks", "PKa,PKb"); err != nil {
		t.Fatalf("flag set: %v", err)
	}

	in := strings.Join([]string{
		"#PKGENV=true",
		"#ISHYPERVISOR=true",
		"#HYPERVISORPKS=('')",
		"",
	}, "\n")
	got := applyFlagsToConf(in, cmd)

	for _, want := range []string{
		"PKGENV=true",
		"ISHYPERVISOR=true",
		"HYPERVISORPKS=('PKa' 'PKb')",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output; got:\n%s", want, got)
		}
	}
	// Negative: none of the original commented forms should survive.
	for _, gone := range []string{
		"#PKGENV=true",
		"#ISHYPERVISOR=true",
		"#HYPERVISORPKS=",
	} {
		if strings.Contains(got, gone) {
			t.Errorf("expected commented form %q to be replaced; got:\n%s", gone, got)
		}
	}
}
