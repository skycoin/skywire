// Package cmdutil pkg/cmdutil/skyenv_test.go
//
// Coverage for the SKYENV native-Go parser. The expressions exercised
// here are exactly those that `cli config gen` issues at flag-binding
// time — see cmd/skywire-cli/commands/config/gen.go for the live
// callsites. Adding a new expression shape to a template should land
// alongside a new case in these tests.
package cmdutil

import (
	"os"
	"path/filepath"
	"testing"
)

// envWith writes a temporary env file containing the given body and
// returns its path. Caller-side cleanup happens via t.TempDir().
func envWith(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "skywire.conf")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestSkyenvParseAssignments(t *testing.T) {
	body := `# comment line
PKGENV=true
USRENV=false
OUTPUT='./skywire-config.json'
SKYCHATADDR=":8001"
SVCCONFADDR=('http://a' 'http://b')
HYPERVISORPKS=('pk1' 'pk2' 'pk3')
SVCCONF=services-config.json   # trailing comment
EMPTY=
QUOTED_SPACE='hello world'
NUM=42
`
	path := envWith(t, body)
	f, err := ParseSkyenvFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	check := func(key string, want []string) {
		t.Helper()
		got, ok := f.Vars[key]
		if !ok {
			t.Errorf("%s: missing", key)
			return
		}
		if len(got) != len(want) {
			t.Errorf("%s: len got=%d want=%d (%v)", key, len(got), len(want), got)
			return
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%s[%d]: got %q want %q", key, i, got[i], want[i])
			}
		}
	}
	check("PKGENV", []string{"true"})
	check("USRENV", []string{"false"})
	check("OUTPUT", []string{"./skywire-config.json"})
	check("SKYCHATADDR", []string{":8001"})
	check("SVCCONFADDR", []string{"http://a", "http://b"})
	check("HYPERVISORPKS", []string{"pk1", "pk2", "pk3"})
	check("SVCCONF", []string{"services-config.json"})
	check("EMPTY", []string{""})
	check("QUOTED_SPACE", []string{"hello world"})
	check("NUM", []string{"42"})
}

func TestSkyenvEvalScalar(t *testing.T) {
	path := envWith(t, "PKGENV=true\nUSRENV=false\nEMPTY=\n")
	cases := []struct {
		expr string
		want string
	}{
		{"${PKGENV}", "true"},
		{"${PKGENV:-false}", "true"},
		{"${USRENV:-true}", "false"}, // set, even though value differs from default
		{"${MISSING}", ""},           // unset, no default
		{"${MISSING:-fallback}", "fallback"},
		{"${MISSING-fallback}", "fallback"},
		{"${EMPTY:-fallback}", "fallback"}, // empty → :- triggers default
		{"plain text", "plain text"},       // non-${…} pass-through
	}
	for _, c := range cases {
		got := SkyenvString(c.expr, path)
		if got != c.want {
			t.Errorf("SkyenvString(%q) = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestSkyenvEvalBool(t *testing.T) {
	path := envWith(t, "PKGENV=true\nUSRENV=false\n")
	if !SkyenvBool("${PKGENV:-false}", path) {
		t.Error("PKGENV=true should resolve to true")
	}
	if SkyenvBool("${USRENV:-true}", path) {
		t.Error("USRENV=false should resolve to false")
	}
	if SkyenvBool("${MISSING:-false}", path) {
		t.Error("missing var w/ default false should be false")
	}
	if !SkyenvBool("${MISSING:-true}", path) {
		t.Error("missing var w/ default true should be true")
	}
}

func TestSkyenvEvalArray(t *testing.T) {
	path := envWith(t, `HYPERVISORPKS=('a' 'b' 'c')
EMPTY_ARR=('')
SVCCONFADDR=('http://x')
`)
	if got := SkyenvArray("${HYPERVISORPKS[@]}", path); got != "a,b,c" {
		t.Errorf("HYPERVISORPKS array got %q want a,b,c", got)
	}
	if got := SkyenvStringSlice("${HYPERVISORPKS[@]}", path); len(got) != 3 || got[1] != "b" {
		t.Errorf("HYPERVISORPKS slice got %v want [a b c]", got)
	}
	// `EMPTY_ARR=('')` parses as a single-element array containing
	// an empty string. Eval treats single-empty-element as "use
	// default" so a template that ships `KEY=('')` (the canonical
	// "placeholder, fill me in" form in skywire.conf) still gets
	// the operator's `:-default` when nothing's been filled in.
	if got := SkyenvArray("${EMPTY_ARR[@]-defx}", path); got != "defx" {
		t.Errorf("EMPTY_ARR[@]-default got %q want defx", got)
	}
	if got := SkyenvArray("${MISSING_ARR[@]-fallback}", path); got != "fallback" {
		t.Errorf("MISSING_ARR[@]-fallback got %q want fallback", got)
	}
	if got := SkyenvArray("${SVCCONFADDR[@]-https://default}", path); got != "http://x" {
		t.Errorf("SVCCONFADDR[@]-default got %q want http://x", got)
	}
}

func TestSkyenvEvalInt(t *testing.T) {
	path := envWith(t, "MINDMSGSESS=8\n")
	if got := SkyenvInt("${MINDMSGSESS:-2}", path); got != 8 {
		t.Errorf("MINDMSGSESS got %d want 8", got)
	}
	if got := SkyenvInt("${MISSING:-2}", path); got != 2 {
		t.Errorf("missing w/ default 2 got %d want 2", got)
	}
	if got := SkyenvInt("${NONNUMERIC:-bogus}", path); got != 0 {
		t.Errorf("unparseable default got %d want 0", got)
	}
}

func TestSkyenvCommentAndQuoteHandling(t *testing.T) {
	path := envWith(t, `# this is a comment
   # indented comment
KEY1=value1
KEY2='quoted value'
KEY3="dq value"
KEY4=trailing # this is comment, not value
KEY5='value with # not a comment'
KEY6=
not an assignment
=alsoNot
123BadName=ignored
`)
	f, _ := ParseSkyenvFile(path) //nolint:errcheck
	cases := map[string]string{
		"KEY1": "value1",
		"KEY2": "quoted value",
		"KEY3": "dq value",
		"KEY4": "trailing",
		"KEY5": "value with # not a comment",
		"KEY6": "",
	}
	for k, want := range cases {
		got := f.Vars[k]
		if len(got) != 1 || got[0] != want {
			t.Errorf("%s: got %v want [%q]", k, got, want)
		}
	}
	if _, ok := f.Vars["123BadName"]; ok {
		t.Error("identifier starting with digit should be rejected")
	}
	if _, ok := f.Vars[""]; ok {
		t.Error("`=alsoNot` should not produce an empty-name entry")
	}
}

func TestSkyenvMissingFile(t *testing.T) {
	// Missing file is not an error — mirrors the old bash path
	// where a non-existent SKYENV just produced empty expansions.
	f, err := ParseSkyenvFile("/no/such/path/skywire.conf")
	if err != nil {
		t.Errorf("missing file should return nil err, got %v", err)
	}
	if len(f.Vars) != 0 {
		t.Errorf("missing file should yield empty vars, got %v", f.Vars)
	}
	if got := SkyenvBool("${PKGENV:-true}", ""); !got {
		t.Errorf("empty envfile + default true should be true, got %v", got)
	}
}

func TestSkyenvRedirect(t *testing.T) {
	// One level of SKYENV= redirect honored: the original file is
	// parsed first, then if it sets SKYENV to a different existing
	// file, that file's assignments overlay the original.
	dir := t.TempDir()
	pri := filepath.Join(dir, "primary.conf")
	sec := filepath.Join(dir, "override.conf")
	_ = os.WriteFile(pri, []byte("PKGENV=true\nSK=primary_sk\nSKYENV="+sec+"\n"), 0o600) //nolint:errcheck,gosec
	_ = os.WriteFile(sec, []byte("SK=override_sk\nUSRENV=true\n"), 0o600)                //nolint:errcheck,gosec

	if got := SkyenvString("${SK}", pri); got != "override_sk" {
		t.Errorf("SK after redirect got %q want override_sk", got)
	}
	// PKGENV not overridden in `sec`, so it should still be `true`
	// from the primary file.
	if got := SkyenvBool("${PKGENV:-false}", pri); !got {
		t.Errorf("PKGENV after redirect got %v want true", got)
	}
	if got := SkyenvBool("${USRENV:-false}", pri); !got {
		t.Errorf("USRENV (set only in override) got %v want true", got)
	}
}
