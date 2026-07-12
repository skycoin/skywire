package server

import (
	"errors"
	"testing"
)

// TestAllocUniqueAmountRetries verifies the allocator retries past collisions
// and returns a non-colliding amount.
func TestAllocUniqueAmountRetries(t *testing.T) {
	calls := 0
	// Report the first 3 generated amounts as taken, then free.
	exists := func(float64) (bool, error) {
		calls++
		return calls <= 3, nil
	}
	amt, err := allocUniqueAmount(10, skyDecimals, exists)
	if err != nil {
		t.Fatalf("allocUniqueAmount: %v", err)
	}
	if amt <= 10 {
		t.Fatalf("amount %v should be a non-round value above the base", amt)
	}
	if calls != 4 {
		t.Fatalf("expected 4 attempts (3 collisions + 1 free), got %d", calls)
	}
}

// TestAllocUniqueAmountExhausts returns an error when every amount collides.
func TestAllocUniqueAmountExhausts(t *testing.T) {
	exists := func(float64) (bool, error) { return true, nil }
	if _, err := allocUniqueAmount(10, skyDecimals, exists); !errors.Is(err, errNoUniqueAmount) {
		t.Fatalf("expected errNoUniqueAmount, got %v", err)
	}
}

// TestAllocUniqueAmountPropagatesError surfaces a DB error from the check.
func TestAllocUniqueAmountPropagatesError(t *testing.T) {
	boom := errors.New("db down")
	exists := func(float64) (bool, error) { return false, boom }
	if _, err := allocUniqueAmount(10, skyDecimals, exists); !errors.Is(err, boom) {
		t.Fatalf("expected the db error, got %v", err)
	}
}

// TestNonRoundPrecision confirms SKY amounts round to 6 decimals (droplets).
func TestNonRoundPrecision(t *testing.T) {
	for i := 0; i < 1000; i++ {
		v := nonRound(10, skyDecimals)
		// v scaled by 1e6 must be a whole number of droplets.
		scaled := v * 1e6
		if scaled != float64(int64(scaled+0.5)) {
			t.Fatalf("SKY amount %v is not a whole number of droplets", v)
		}
		if v <= 10 || v > 10.001 {
			t.Fatalf("delta out of expected small range: %v", v)
		}
	}
}
