package cmdutil

import (
	"os"
	"path/filepath"
	"testing"
)

// Windows installs were given a PowerShell form of the template, which the
// parser did not read, so every setting in it was ignored.
func TestParsePowerShellForm(t *testing.T) {
	p := filepath.Join(t.TempDir(), "skywire.conf")
	src := "$PKGENV=$true\n$NOWALLET=$False\n$LOGLVL='debug'\n$HYPERVISORPKS=@('aa','bb')\n#$ISHYPERVISOR=$true\nWSPEERS=('c,d' 'e')\n"
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	for expr, want := range map[string]string{
		"${PKGENV}":        "true",
		"${NOWALLET}":      "false",
		"${LOGLVL}":        "debug",
		"${ISHYPERVISOR}":  "",
		"${HYPERVISORPKS}": "aa",
	} {
		if got := SkyenvString(expr, p); got != want {
			t.Errorf("%s = %q, want %q", expr, got, want)
		}
	}
	if got := SkyenvArray("${HYPERVISORPKS[@]}", p); got != "aa,bb" {
		t.Errorf("HYPERVISORPKS = %q", got)
	}
	if got := SkyenvArray("${WSPEERS[@]}", p); got != "c,d,e" {
		t.Errorf("a bash array keeps a quoted comma: %q", got)
	}
}
