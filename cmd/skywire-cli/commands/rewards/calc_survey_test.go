package clirewards

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/skycoin/skywire/pkg/visor/rewardconfig"
)

// A survey is written by the visor it describes, so its fields must not be
// able to break a CSV row or slip an xpub past the address check.
func TestParseSurveyCleansReportedFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node-info.json")
	body := `{"skycoin_address":" xpub6abc\n","uuid":"id-1\n2,3","ip_address":"\t1.2.3.4 ","go_arch":"amd64,evil"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := parseSurvey(path)
	if err != nil {
		t.Fatal(err)
	}
	for got, want := range map[string]string{s.SkyAddr: "xpub6abc", s.UUID: "id-123", s.IPAddr: "1.2.3.4", s.Arch: "amd64evil"} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestPaddedAddressIsTheSameAddress(t *testing.T) {
	const addr = "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6"
	want, _, err := rewardconfig.ValidateRewardAddress(addr)
	if err != nil {
		t.Fatal(err)
	}
	if got := csvSafe(" " + addr + "\r\n"); got != want {
		t.Errorf("padded address reads as %q, want %q", got, want)
	}
}
