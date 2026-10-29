package autoconfigcmd

import (
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestEnvChecksNameRealVariables keeps the rule table in step with envMap, so
// a renamed variable cannot quietly lose its check.
func TestEnvChecksNameRealVariables(t *testing.T) {
	keys := map[string]bool{}
	for _, m := range envMap {
		keys[m.Key] = true
	}
	for k := range envChecks {
		if !keys[k] {
			t.Errorf("envChecks names %s, which no autoconfig flag writes", k)
		}
	}
}

// TestDefaultsPassTheirChecks makes sure autoconfig never refuses a value it
// would write by default.
func TestDefaultsPassTheirChecks(t *testing.T) {
	for _, f := range Describe() {
		if f.Check == "" {
			continue
		}
		for _, v := range []string{f.DefaultVal, f.EnvDefault} {
			if err := checkValue(f.Check, v); err != nil {
				t.Errorf("--%s default %q fails its %s check: %v", f.Name, v, f.Check, err)
			}
		}
	}
}

func TestCheckValue(t *testing.T) {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	good := map[string][]string{
		CheckPK:       {pk1.Hex(), ""},
		CheckPKs:      {pk1.Hex() + "," + pk2.Hex(), pk1.Hex() + ", " + pk2.Hex(), ""},
		CheckHostPort: {"127.0.0.1:8000", ":8000", "[::1]:80", "example.com:443"},
		CheckDuration: {"10s", "5m", "1h30m"},
		CheckLogLevel: {"debug", "info", "warn", "error"},
	}
	bad := map[string][]string{
		CheckPK:       {"02abc", pk1.Hex() + "x"},
		CheckPKs:      {pk1.Hex() + ",nope", "02abc"},
		CheckHostPort: {"127.0.0.1", "host:port", "h:70000"},
		CheckDuration: {"10", "soon"},
		CheckLogLevel: {"loud"},
	}
	for rule, vals := range good {
		for _, v := range vals {
			if err := checkValue(rule, v); err != nil {
				t.Errorf("%s %q: unexpected error %v", rule, v, err)
			}
		}
	}
	for rule, vals := range bad {
		for _, v := range vals {
			if err := checkValue(rule, v); err == nil {
				t.Errorf("%s %q: accepted, want an error", rule, v)
			}
		}
	}
}

// TestEditForRefusesABadValue checks the rule is applied where both the CLI
// and the visor settings page turn a flag value into a .conf edit.
func TestEditForRefusesABadValue(t *testing.T) {
	if _, err := EditFor("hvpks", "notakey"); err == nil || !strings.Contains(err.Error(), "--hvpks") {
		t.Fatalf("EditFor(hvpks, notakey) = %v, want a --hvpks error", err)
	}
	pk, _ := cipher.GenerateKeyPair()
	if _, err := EditFor("hvpks", pk.Hex()); err != nil {
		t.Fatalf("EditFor(hvpks, valid) = %v", err)
	}
}
