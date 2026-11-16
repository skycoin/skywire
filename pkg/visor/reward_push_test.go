package visor

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// A kick pushes at once, long before the warmup ends.
func TestRewardPushLoopKick(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	kick := make(chan struct{}, 1)
	pushed := make(chan struct{}, 4)
	go rewardPushLoop(ctx, kick, time.Hour, time.Hour, func() { pushed <- struct{}{} })

	kick <- struct{}{}
	select {
	case <-pushed:
	case <-time.After(time.Second):
		t.Fatal("kick did not push")
	}
	select {
	case <-pushed:
		t.Fatal("pushed again without a kick")
	case <-time.After(50 * time.Millisecond):
	}
}

func newRewardPushVisor(t *testing.T) *Visor {
	t.Helper()
	return &Visor{
		conf:   &visorconfig.V1{LocalPath: t.TempDir()},
		log:    logging.MustGetLogger("test"),
		survey: surveyState{mu: new(sync.RWMutex), kick: make(chan struct{}, 1)},
	}
}

// Setting the reward address regenerates the survey and asks for a push.
func TestSetRewardAddressPushesNow(t *testing.T) {
	const xpub = "xpub6DMLxE9HpaMf7wCMK2ikiAshrkvHBWfdyFFhdajSpi1h2PvCN51rLzxfsqV9EPnrmqpRmnutibiKJX39acErEGmU5syK9ir6wmMAnc2XQWL"
	old := generateSurvey
	t.Cleanup(func() { generateSurvey = old })
	pk, _ := cipher.GenerateKeyPair()
	generateSurvey = func(v *Visor, _ *logging.Logger, _ bool) {
		v.survey.mu.Lock()
		v.survey.data = visorconfig.Survey{PubKey: pk, SkycoinAddress: v.conf.RewardAddress}
		v.survey.mu.Unlock()
	}
	v := newRewardPushVisor(t)
	if _, err := v.SetRewardAddress(xpub); err != nil {
		t.Fatal(err)
	}
	select {
	case <-v.survey.kick:
	case <-time.After(time.Second):
		t.Fatal("no push after setting the reward address")
	}
	v.survey.mu.RLock()
	defer v.survey.mu.RUnlock()
	if v.survey.data.SkycoinAddress != xpub {
		t.Fatalf("survey has %q", v.survey.data.SkycoinAddress)
	}
}

// Deleting it asks for a push of the last survey without the address.
func TestDeleteRewardAddressPushesOptOut(t *testing.T) {
	const xpub = "xpub6DMLxE9HpaMf7wCMK2ikiAshrkvHBWfdyFFhdajSpi1h2PvCN51rLzxfsqV9EPnrmqpRmnutibiKJX39acErEGmU5syK9ir6wmMAnc2XQWL"
	v := newRewardPushVisor(t)
	pk, _ := cipher.GenerateKeyPair()
	if err := v.storeRewardAddress(xpub); err != nil {
		t.Fatal(err)
	}
	v.survey.data = visorconfig.Survey{PubKey: pk, SkycoinAddress: xpub, IPAddr: "1.2.3.4"}
	if err := v.DeleteRewardAddress(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-v.survey.kick:
	default:
		t.Fatal("no push after deleting the reward address")
	}
	if o := v.survey.optOut; o.PubKey != pk || o.SkycoinAddress != "" || o.IPAddr != "1.2.3.4" {
		t.Fatalf("opt-out survey %+v", o)
	}
}
