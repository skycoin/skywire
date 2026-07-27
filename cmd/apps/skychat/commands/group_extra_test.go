// Package commands cmd/apps/skychat/commands/group_extra_test.go
//
// Unit coverage for two group helpers that don't need a live mesh: parsePKList
// (the hex-PK list parser) and collectGroupHealth (the /status group-health
// projection over the visor GroupList RPC — disabled, live-mapping, and
// rpc-error paths, using a fake pair RPC).
package commands

import (
	"errors"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor"
)

func TestParsePKList(t *testing.T) {
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()

	// Blank + whitespace-only entries are skipped; surrounding space is trimmed.
	got, err := parsePKList([]string{pkA.Hex(), "  ", "", "  " + pkB.Hex() + " "})
	if err != nil {
		t.Fatalf("parsePKList: %v", err)
	}
	if len(got) != 2 || got[0] != pkA || got[1] != pkB {
		t.Errorf("parsePKList = %v, want [%s %s]", got, pkA.Hex(), pkB.Hex())
	}

	// An all-blank list yields an empty (non-nil) slice, no error.
	if got, err := parsePKList([]string{"", "   "}); err != nil || len(got) != 0 {
		t.Errorf("all-blank: got=%v err=%v, want empty", got, err)
	}

	// A malformed entry is a hard error.
	if _, err := parsePKList([]string{pkA.Hex(), "not-a-pubkey"}); err == nil {
		t.Error("parsePKList should error on a malformed key")
	}
}

// groupListErrAPI is a fake visor.API whose GroupList RPC fails, exercising
// collectGroupHealth's rpc-error branch.
type groupListErrAPI struct{ visorAPIShim }

func (groupListErrAPI) GroupList() ([]visor.GroupInfo, error) {
	return nil, errors.New("upstream GroupList boom")
}

func TestCollectGroupHealth_Disabled(t *testing.T) {
	// pairRPC unavailable → empty list + the disabled reason, never a nil slice.
	pairRPCMu.Lock()
	prev := pairRPC
	pairRPC = nil
	pairRPCMu.Unlock()
	t.Cleanup(func() {
		pairRPCMu.Lock()
		pairRPC = prev
		pairRPCMu.Unlock()
	})

	out, reason := collectGroupHealth()
	if out == nil || len(out) != 0 || reason != "pair-rpc-disabled" {
		t.Errorf("disabled: out=%v reason=%q, want empty + pair-rpc-disabled", out, reason)
	}
}

func TestCollectGroupHealth_LiveMapping(t *testing.T) {
	m1, _ := cipher.GenerateKeyPair()
	m2, _ := cipher.GenerateKeyPair()
	now := time.Now().UTC()
	fake := &groupAPI{groups: []visor.GroupInfo{
		{
			ID:              "g1",
			Name:            "one",
			Members:         []cipher.PubKey{m1, m2},
			LastMessageAt:   now.Add(-30 * time.Second),
			SubscriberAlive: true,
			SubDropCount:    3,
		},
		{ID: "g2", Name: "two"}, // no members, zero LastMessageAt
	}}
	withFakePairRPC(t, fake)

	out, reason := collectGroupHealth()
	if reason != "" {
		t.Fatalf("live: unexpected reason %q", reason)
	}
	if len(out) != 2 {
		t.Fatalf("live: got %d groups, want 2", len(out))
	}

	g1 := out[0]
	if g1.ID != "g1" || g1.Name != "one" || g1.MembersCount != 2 || !g1.SubscriberAlive || g1.SubDropCount != 3 {
		t.Errorf("g1 mapping = %+v", g1)
	}
	// A non-zero LastMessageAt yields a computed, non-negative lag.
	if g1.LagSeconds == nil || *g1.LagSeconds < 0 {
		t.Errorf("g1 LagSeconds = %v, want non-nil non-negative", g1.LagSeconds)
	}

	g2 := out[1]
	if g2.ID != "g2" || g2.MembersCount != 0 {
		t.Errorf("g2 mapping = %+v", g2)
	}
	// A zero LastMessageAt leaves lag unset (nil), never a bogus huge value.
	if g2.LagSeconds != nil {
		t.Errorf("g2 LagSeconds = %v, want nil for a group with no messages", *g2.LagSeconds)
	}
}

func TestCollectGroupHealth_RPCError(t *testing.T) {
	withChatLog(t) // the rpc-error branch logs at debug level
	withFakePairRPC(t, groupListErrAPI{})

	out, reason := collectGroupHealth()
	if out == nil || len(out) != 0 {
		t.Errorf("rpc-error: out=%v, want empty slice", out)
	}
	if len(reason) < len("rpc-error:") || reason[:len("rpc-error:")] != "rpc-error:" {
		t.Errorf("rpc-error: reason=%q, want an 'rpc-error:' prefix", reason)
	}
}
