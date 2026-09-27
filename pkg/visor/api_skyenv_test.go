// Package visor pkg/visor/api_skyenv_test.go c3-vis-core
package visor

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/skywireconfig/skyenvfile"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

func TestSkyenvRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "skywire.conf")
	t.Setenv(skyenvfile.SKYENVVar, p)
	v := &Visor{}

	st, err := v.Skyenv()
	if err != nil || st.Exists {
		t.Fatalf("missing file: exists=%v err=%v", st.Exists, err)
	}
	if _, err := v.SetSkyenv(visorapi.SkyenvEdits{Set: map[string]string{"ishv": "true"}}); err == nil {
		t.Fatal("editing a missing file should fail, not create it")
	}

	src := "PKGENV=true\n#ISHYPERVISOR=true\nSK='00aa'\n#VPNROUTERPASSPHRASE=''\n"
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err = v.SetSkyenv(visorapi.SkyenvEdits{
		Set:   map[string]string{"ishv": "true", "vpnrouter-passphrase": "hunter22"},
		Unset: []string{"public"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !st.Writable {
		t.Error("temp dir should be writable")
	}
	if st.Values["ISHYPERVISOR"] != "true" {
		t.Errorf("ISHYPERVISOR = %q", st.Values["ISHYPERVISOR"])
	}
	for _, k := range []string{"SK", "VPNROUTERPASSPHRASE"} {
		if st.Values[k] != SkyenvRedacted {
			t.Errorf("%s leaked: %q", k, st.Values[k])
		}
	}
	if len(st.Flags) == 0 {
		t.Error("no flag metadata")
	}

	if _, err := v.SetSkyenv(visorapi.SkyenvEdits{Set: map[string]string{"vpnrouter-passphrase": SkyenvRedacted}}); err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, p); !strings.Contains(got, "VPNROUTERPASSPHRASE='hunter22'") {
		t.Errorf("echoed redaction overwrote the secret:\n%s", got)
	}

	if _, err := v.SetSkyenv(visorapi.SkyenvEdits{Set: map[string]string{"sk": "11bb"}}); err == nil {
		t.Error("sk edit should be refused")
	}
	if _, err := v.SetSkyenv(visorapi.SkyenvEdits{Set: map[string]string{"nosuchflag": "1"}}); err == nil {
		t.Error("unknown flag should be refused")
	}
	got := readTestFile(t, p)
	if !strings.Contains(got, "SK='00aa'") {
		t.Errorf("SK changed:\n%s", got)
	}
}

func TestSkyenvWritableRootOwnedEtc(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() == 0 {
		t.Skip("needs an unprivileged user on linux")
	}
	if skyenvWritable("/etc/skywire.conf") {
		t.Error("an unprivileged visor reported /etc writable")
	}
}

func readTestFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p) //nolint:gosec // G304: this test's own temp file
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
