package skyenvfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A PowerShell-form line is read, and an edit rewrites it in the bash form.
func TestPowerShellFormValuesAndUpdate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "skywire.conf")
	if err := os.WriteFile(p, []byte("$PKGENV=$true\n$HYPERVISORPKS=@('aa','bb')\n#$LOGLVL='info'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	vals, err := Values(p)
	if err != nil {
		t.Fatal(err)
	}
	if vals["PKGENV"] != "true" || vals["HYPERVISORPKS"] != "aa bb" {
		t.Errorf("values = %v", vals)
	}
	if err := Update(p, []Edit{{Key: "LOGLVL", Value: FormatString("debug")}}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); !strings.Contains(got, "LOGLVL='debug'") || strings.Contains(got, "$LOGLVL") {
		t.Errorf("after update:\n%s", got)
	}
}
