package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/skywireconfig/autoconfigcmd"
)

// TestHelpShowsSkyenvValues checks --help shows the values in the skywire.conf
// autoconfig would edit, and leaves a commented variable at the flag default.
func TestHelpShowsSkyenvValues(t *testing.T) {
	conf := filepath.Join(t.TempDir(), "skywire.conf")
	body := "SKYMAILBRIDGE=true\nTRANSPORTPORT=9083\nHYPERVISORPKS=('02aa' '02bb')\n#DMSGWEB=true\n"
	if err := os.WriteFile(conf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SKYENV", conf)

	c := autoconfigcmd.New(&autoconfigcmd.Values{})
	showSkyenvDefaults(c)

	want := map[string]string{"skymail-bridge": "true", "transport-port": "9083", "hvpks": "02aa,02bb", "dmsgweb": "false"}
	for name, v := range want {
		if got := c.Flags().Lookup(name).DefValue; got != v {
			t.Errorf("--%s default = %q, want %q", name, got, v)
		}
	}
	if got := c.Flags().Lookup("no-skymail-bridge").DefValue; got != "false" {
		t.Errorf("--no-skymail-bridge default = %q, want it left at false", got)
	}
	if !strings.Contains(c.Long, conf) {
		t.Errorf("help does not name %s", conf)
	}
}
