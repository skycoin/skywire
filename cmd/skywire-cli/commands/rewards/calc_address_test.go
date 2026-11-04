package clirewards

import (
	"errors"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/visor/rewardconfig"
)

func TestRewardAddressCutoff(t *testing.T) {
	const addr = "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6"
	const xpub = "xpub6CcSesQqGHf226EZjBZjRTkTrTPmQ8oCAjAHtDBNpYTj1Yu1v46V5TaYEZeSE8iyHEj3eXs7ozZok4ux9b5uLHZRtHacskodD7B53xRG6sE"
	before := time.Date(2026, time.November, 14, 0, 0, 0, 0, time.UTC)

	if err := rewardAddressError(addr, before); err != nil {
		t.Fatalf("single address before the cutoff: %v", err)
	}
	err := rewardAddressError(addr, singleAddressCutoff)
	if !errors.Is(err, rewardconfig.ErrSingleAddress) || addressReason(err) != "Single address, xpub required" {
		t.Fatalf("single address on the cutoff: %v", err)
	}
	if err := rewardAddressError(xpub, singleAddressCutoff); err != nil {
		t.Fatalf("xpub on the cutoff: %v", err)
	}
	if err := rewardAddressError("notanaddress", before); err == nil || addressReason(err) != "Invalid Skycoin address" {
		t.Fatalf("invalid address: %v", err)
	}
}
