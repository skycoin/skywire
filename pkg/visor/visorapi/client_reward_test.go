package visorapi

import (
	"errors"
	"testing"
)

func TestSingleRewardErr(t *testing.T) {
	if err := singleRewardErr(errors.New("rpc: can't find method RPC.SetSingleRewardAddress")); !errors.Is(err, ErrSingleRewardUnsupported) {
		t.Fatalf("old build: %v", err)
	}
	other := errors.New("this address has already sent coins")
	if err := singleRewardErr(other); err != other {
		t.Fatalf("other error: %v", err)
	}
	if singleRewardErr(nil) != nil {
		t.Fatal("nil error")
	}
}
