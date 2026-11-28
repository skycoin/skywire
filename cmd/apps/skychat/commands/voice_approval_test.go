package commands

import (
	"slices"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

type approvalAPI struct {
	visorAPIShim
	pushes []visorapi.VoiceApproval
}

func (a *approvalAPI) VoiceSetApproval(approval visorapi.VoiceApproval) error {
	a.pushes = append(a.pushes, approval)
	return nil
}

// With approval off the visor is told so and every caller rings. With it on,
// only the accepted and the named are let in, so a stranger is declined.
func TestVoice_CallApprovalReachesTheVisor(t *testing.T) {
	withPrivacyStore(t)
	book := withContactStore(t)
	prev := approvalPush
	t.Cleanup(func() { approvalPush = prev })
	approvalPush.on, approvalPush.list, approvalPush.at = false, nil, time.Time{}
	fake := &approvalAPI{}
	withPairRPC(t, fake)

	now := time.Now()
	pushCallApproval(now)
	if len(fake.pushes) != 1 || fake.pushes[0].On {
		t.Fatalf("approval off: pushes = %+v, want one with On false", fake.pushes)
	}

	accepted, _ := cipher.GenerateKeyPair()
	named, _ := cipher.GenerateKeyPair()
	stranger, _ := cipher.GenerateKeyPair()
	if err := privacyStore.SetApproval(true, []string{accepted.Hex()}); err != nil {
		t.Fatal(err)
	}
	if _, err := book.Set(named.Hex(), "friend"); err != nil {
		t.Fatal(err)
	}
	pushCallApproval(now.Add(time.Second))
	if len(fake.pushes) != 2 {
		t.Fatalf("pushes = %d, want 2", len(fake.pushes))
	}
	got := fake.pushes[1]
	if !got.On || !slices.Contains(got.Accepted, accepted) || !slices.Contains(got.Accepted, named) || slices.Contains(got.Accepted, stranger) {
		t.Fatalf("approval on: pushed %+v, want On with the accepted and named peers only", got)
	}
}

// A refused call is logged once as a missed call, however many polls list it.
func TestVoice_RefusedCallsLoggedOnce(t *testing.T) {
	peer, _ := cipher.GenerateKeyPair()
	logged := map[string]bool{}
	var records []string
	record := func(p, outcome string, _ time.Duration) { records = append(records, p+" "+outcome) }
	refused := []visorapi.VoiceRefusedInfo{{CallID: "c1", Peer: peer.Hex(), Reason: "busy"}}
	recordRefusedCalls(logged, refused, record)
	recordRefusedCalls(logged, refused, record)
	if len(records) != 1 || records[0] != peer.Hex()+" "+callMissed {
		t.Fatalf("records = %v, want one missed call", records)
	}
	recordRefusedCalls(logged, nil, record)
	if len(logged) != 0 {
		t.Fatalf("logged = %v, want pruned once the visor stops listing it", logged)
	}
}
