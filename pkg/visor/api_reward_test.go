package visor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// TestDeleteRewardAddress checks that a removed address is gone from what the
// running visor reports, not only from reward.txt.
func TestDeleteRewardAddress(t *testing.T) {
	const xpub = "xpub6DMLxE9HpaMf7wCMK2ikiAshrkvHBWfdyFFhdajSpi1h2PvCN51rLzxfsqV9EPnrmqpRmnutibiKJX39acErEGmU5syK9ir6wmMAnc2XQWL"
	dir := t.TempDir()
	path := filepath.Join(dir, skyenv.RewardFile)
	if err := os.WriteFile(path, []byte(xpub), 0600); err != nil {
		t.Fatal(err)
	}
	v := &Visor{
		conf:   &visorconfig.V1{LocalPath: dir, RewardAddress: xpub},
		survey: surveyState{mu: new(sync.RWMutex), data: visorconfig.Survey{SkycoinAddress: xpub}},
	}

	if err := v.DeleteRewardAddress(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("reward.txt still there: %v", err)
	}
	if got, err := v.GetRewardAddress(); err != nil || got != "" {
		t.Fatalf("GetRewardAddress after delete = %q, %v", got, err)
	}
	if v.survey.data.SkycoinAddress != "" {
		t.Fatalf("survey still reports %q", v.survey.data.SkycoinAddress)
	}
}

func TestGetRewardXpubAddresses(t *testing.T) {
	const (
		account = "xpub6CcSesQqGHf226EZjBZjRTkTrTPmQ8oCAjAHtDBNpYTj1Yu1v46V5TaYEZeSE8iyHEj3eXs7ozZok4ux9b5uLHZRtHacskodD7B53xRG6sE"
		chain   = "xpub6EMqT6hKxEDgyojE3BRxD9i59YPDpFkJSJxyUpEW1XDnc35kR3G2h25V5yvgfoQUT7AdAU6ATwjcPBBnjS1fNvR6VFECdpBWLQ6puzXZPGr"
	)
	h := (&Hypervisor{}).getRewardXpubAddresses()
	get := func(xpub string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet, "/reward-xpub-addresses?xpub="+url.QueryEscape(xpub), nil))
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body) //nolint:errcheck
		return rec.Code, body
	}
	code, body := get(account)
	addrs, _ := body["addresses"].([]any)
	if code != http.StatusOK || len(addrs) != 5 || addrs[0] != "2G2YuFkkDg3NbeDX2SNycEztgc6Sy6x9yHq" {
		t.Fatalf("account xpub: %d %v", code, body)
	}
	for _, bad := range []string{chain, "2G2YuFkkDg3NbeDX2SNycEztgc6Sy6x9yHq", ""} {
		if code, body := get(bad); code != http.StatusBadRequest {
			t.Fatalf("%q: %d %v", bad, code, body)
		}
	}
}
