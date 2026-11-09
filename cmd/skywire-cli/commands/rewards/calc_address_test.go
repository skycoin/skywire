package clirewards

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	deprewards "github.com/skycoin/skywire/pkg/deployment/rewards"
	"github.com/skycoin/skywire/pkg/visor/rewardconfig"
)

func TestRewardAddressCutoff(t *testing.T) {
	const unspent = "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6"
	const spent = "2VfPAT8tZMwoypWSZgdhgCiKrYdYoSxy1dE"
	const xpub = "xpub6CcSesQqGHf226EZjBZjRTkTrTPmQ8oCAjAHtDBNpYTj1Yu1v46V5TaYEZeSE8iyHEj3eXs7ozZok4ux9b5uLHZRtHacskodD7B53xRG6sE"
	before := time.Date(2026, time.November, 14, 0, 0, 0, 0, time.UTC)

	queries := 0
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries++
		switch r.URL.Query().Get("address") {
		case unspent:
			w.Write([]byte(`[{"coins":1000000,"spent_block_seq":0}]`)) //nolint:errcheck,gosec
		case spent:
			w.Write([]byte(`[{"coins":1000000,"spent_block_seq":0},{"coins":7000000,"spent_block_seq":195400}]`)) //nolint:errcheck,gosec
		default:
			http.Error(w, "unexpected address", http.StatusBadRequest)
		}
	}))
	defer node.Close()
	oldURL := skycoinNodeURL
	skycoinNodeURL = node.URL
	defer func() { skycoinNodeURL = oldURL }()

	// Before the cutoff the node is not asked and any valid address is paid.
	if inel, failed := checkRewardAddress(spent, before); inel != nil || failed != nil || queries != 0 {
		t.Fatalf("spent single address before the cutoff: %v %v (queries %d)", inel, failed, queries)
	}
	if inel, failed := checkRewardAddress(unspent, deprewards.SingleAddressCutoff); inel != nil || failed != nil {
		t.Fatalf("unspent single address on the cutoff: %v %v", inel, failed)
	}
	inel, failed := checkRewardAddress(spent, deprewards.SingleAddressCutoff)
	if failed != nil || !errors.Is(inel, rewardconfig.ErrAddressSpent) || addressReason(inel) != "Reward address has sent coins" {
		t.Fatalf("spent single address on the cutoff: %v %v", inel, failed)
	}
	if inel, failed := checkRewardAddress(xpub, deprewards.SingleAddressCutoff); inel != nil || failed != nil {
		t.Fatalf("xpub on the cutoff: %v %v", inel, failed)
	}
	if inel, failed := checkRewardAddress("notanaddress", before); failed != nil || inel == nil || addressReason(inel) != "Invalid Skycoin address" {
		t.Fatalf("invalid address: %v %v", inel, failed)
	}
	n := queries
	if inel, failed := checkRewardAddress(spent, deprewards.SingleAddressCutoff.AddDate(0, 0, 1)); inel == nil || failed != nil || queries != n {
		t.Fatalf("answer not cached: %v %v, %d queries, want %d", inel, failed, queries, n)
	}

	// A node that cannot answer stops the calculation, for an address not cached.
	node.Close()
	const other = "2iVtHS5ye99Km5PonsB42No3pQRGEURmxyc"
	if inel, failed := checkRewardAddress(other, deprewards.SingleAddressCutoff); failed == nil || inel != nil {
		t.Fatalf("unreachable node: ineligible %v, failed %v", inel, failed)
	}
}
