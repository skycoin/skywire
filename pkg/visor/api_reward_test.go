package visor

import (
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
