package server

import (
	"errors"
	"math"
	"testing"
)

// TestRoundToDecimals normalizes to an asset's on-chain precision.
func TestRoundToDecimals(t *testing.T) {
	cases := []struct {
		in   float64
		d    int
		want float64
	}{
		{10.123456789, skyDecimals, 10.123457},     // SKY: 6 dp
		{2.123456789, paymentDecimals, 2.12345679}, // payment coin: 8 dp
		{0.0000001, skyDecimals, 0},                // below half a droplet -> 0
		{10.0, skyDecimals, 10.0},
	}
	for _, c := range cases {
		if got := roundToDecimals(c.in, c.d); math.Abs(got-c.want) > 1e-12 {
			t.Errorf("roundToDecimals(%v, %d) = %v, want %v", c.in, c.d, got, c.want)
		}
	}
}

// TestNonRoundExactAtCap verifies that at the max allowed amount, base*10^dec is
// still within float64's exact-integer range, so nonRound's delta survives and
// every draw is a whole, above-base droplet (uniqueness not silently lost).
func TestNonRoundExactAtCap(t *testing.T) {
	for i := 0; i < 500; i++ {
		v := nonRound(maxAmountSKY, skyDecimals)
		if scaled := v * 1e6; scaled != math.Trunc(scaled) {
			t.Fatalf("value %v is not a whole number of droplets at the cap", v)
		}
		if v <= maxAmountSKY {
			t.Fatalf("delta was lost at the cap: %v <= %v", v, maxAmountSKY)
		}
	}
}

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
