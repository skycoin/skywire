package clirewards

import (
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestUptimeKeysAppliesTheMinimum(t *testing.T) {
	const day = "2026-11-06"
	path := filepath.Join(t.TempDir(), "ut.txt")
	lines := "full " + day + " 100.00\n" +
		"exact " + day + " 75.00\n" +
		"just-under " + day + " 74.99\n" +
		"one-heartbeat " + day + " 0.35\n" +
		"other-day 2026-11-05 100.00\n" +
		"unreadable " + day + " n/a\n" +
		"short " + day + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}

	met, below, err := uptimeKeys(path, day)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"full", "exact"}; !slices.Equal(met, want) {
		t.Errorf("met = %v, want %v", met, want)
	}
	if want := []string{"just-under", "one-heartbeat", "unreadable"}; !slices.Equal(below, want) {
		t.Errorf("below = %v, want %v", below, want)
	}

	if _, _, err := uptimeKeys(filepath.Join(t.TempDir(), "missing.txt"), day); err == nil {
		t.Error("a missing uptime file must be an error, not an empty day")
	}
}

func setCalcDay(t *testing.T, day string) {
	t.Helper()
	prev := wDate
	d, err := time.Parse("2006-01-02", day)
	if err != nil {
		t.Fatal(err)
	}
	wDate = d
	t.Cleanup(func() { wDate = prev })
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestPerIPShareLimit(t *testing.T) {
	for day, want := range map[string]int{"2026-05-31": 8, "2026-06-01": 12, "2026-06-30": 12, "2026-07-01": 8, "2025-06-15": 8} {
		d, _ := time.Parse("2006-01-02", day) //nolint:errcheck
		if got := perIPShareLimit(d); got != want {
			t.Errorf("perIPShareLimit(%s) = %d, want %d", day, got, want)
		}
	}
}

func TestCalcPresenceShare(t *testing.T) {
	setCalcDay(t, "2026-11-06") // limit 8
	ni := nodeinfo{IPAddr: "ip", MacAddr: "mac"}
	for _, c := range []struct {
		ips, macs int
		want      float64
	}{
		{1, 1, 1},
		{7, 1, 1},
		{8, 1, 1},     // at the limit: 8/8
		{16, 1, 0.5},  // 8/16
		{20, 1, 0.4},  // 8/20
		{1, 2, 0.5},   // one machine, two keys
		{16, 2, 0.25}, // both
	} {
		got := calcPresenceShare(ni, map[string]int{"ip": c.ips}, map[string]int{"mac": c.macs})
		if !near(got, c.want) {
			t.Errorf("ips=%d macs=%d: share = %v, want %v", c.ips, c.macs, got, c.want)
		}
	}

	setCalcDay(t, "2026-06-10") // limit 12
	if got := calcPresenceShare(ni, map[string]int{"ip": 16}, nil); !near(got, 0.75) {
		t.Errorf("June 2026, 16 on one IP: share = %v, want 0.75", got)
	}
}

func TestPool2Weight(t *testing.T) {
	for _, c := range []struct {
		bytes, ipTotal uint64
		exp, want      float64
	}{
		{0, 400, 0.5, 0},
		{100, 0, 0.5, 0},
		{100, 400, 1.0, 100}, // bytes
		{100, 400, 0.5, 5},   // 100 / sqrt(400)
		{100, 400, 0.0, 0.25},
	} {
		if got := pool2Weight(c.bytes, c.ipTotal, c.exp); !near(got, c.want) {
			t.Errorf("pool2Weight(%d, %d, %v) = %v, want %v", c.bytes, c.ipTotal, c.exp, got, c.want)
		}
	}

	// Splitting one IP's bytes across visors must not change the IP's total weight.
	whole := pool2Weight(400, 400, 0.5)
	split := 4 * pool2Weight(100, 400, 0.5)
	if !near(whole, split) {
		t.Errorf("one visor = %v, four visors on the same IP = %v", whole, split)
	}
}

func TestApplyRegionalSaturation(t *testing.T) {
	nodes := func() []nodeinfo {
		return []nodeinfo{
			{Country: "AA", IPAddr: "1", Share: 1},
			{Country: "AA", IPAddr: "2", Share: 1},
			{Country: "AA", IPAddr: "3", Share: 1},
			{Country: "AA", IPAddr: "4", Share: 1},
			{Country: "AA", IPAddr: "4", Share: 0.5}, // same IP: still 4 unique
			{Country: "", IPAddr: "9", Share: 1},     // unknown country, 1 IP
		}
	}

	half := nodes()
	applyRegionalSaturation(half, 0.5) // AA: 4^-0.5 = 0.5, unknown: 1
	for i, want := range []float64{0.5, 0.5, 0.5, 0.5, 0.25, 1} {
		if !near(half[i].Share, want) {
			t.Errorf("exp 0.5 node %d: share = %v, want %v", i, half[i].Share, want)
		}
	}

	zero := nodes()
	applyRegionalSaturation(zero, 0) // AA: 1/4
	if !near(zero[0].Share, 0.25) || !near(zero[5].Share, 1) {
		t.Errorf("exp 0: shares = %v and %v, want 0.25 and 1", zero[0].Share, zero[5].Share)
	}

	off := nodes()
	applyRegionalSaturation(off, 1)
	if !near(off[0].Share, 1) || !near(off[4].Share, 0.5) {
		t.Error("exp 1 must leave shares alone")
	}
}

func TestComputePoolRewards(t *testing.T) {
	nodes := []nodeinfo{{Share: 1}, {Share: 1}, {Share: 2}}
	if total := computePoolRewards(nodes, 400); !near(total, 4) {
		t.Errorf("total shares = %v, want 4", total)
	}
	paid := 0.0
	for i, want := range []float64{100, 100, 200} {
		if !near(nodes[i].Reward, want) {
			t.Errorf("node %d: reward = %v, want %v", i, nodes[i].Reward, want)
		}
		paid += nodes[i].Reward
	}
	if !near(paid, 400) {
		t.Errorf("rewards sum to %v, want the 400 pool", paid)
	}

	// Shares that do not divide the pool evenly still sum to it.
	thirds := []nodeinfo{{Share: 1}, {Share: 1}, {Share: 1}}
	computePoolRewards(thirds, 100)
	if sum := thirds[0].Reward + thirds[1].Reward + thirds[2].Reward; !near(sum, 100) {
		t.Errorf("three equal shares of 100 sum to %v", sum)
	}

	empty := []nodeinfo{{Share: 0}, {Share: 0}}
	if total := computePoolRewards(empty, 400); total != 0 || empty[0].Reward != 0 {
		t.Errorf("a pool with no shares paid: total %v, reward %v", total, empty[0].Reward)
	}
	if total := computePoolRewards(nil, 400); total != 0 {
		t.Errorf("an empty pool has total %v", total)
	}
}

func TestSumRewardsByAddress(t *testing.T) {
	got := sumRewardsByAddress([]nodeinfo{
		{SkyAddr: "a", Reward: 1},
		{SkyAddr: "b", Reward: 5},
		{SkyAddr: "a", Reward: 2.5},
		{SkyAddr: "c", Reward: 4},
	})
	want := []rewardData{{SkyAddr: "b", Reward: 5}, {SkyAddr: "c", Reward: 4}, {SkyAddr: "a", Reward: 3.5}}
	if len(got) != len(want) {
		t.Fatalf("got %d addresses, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].SkyAddr != want[i].SkyAddr || !near(got[i].Reward, want[i].Reward) {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestComputePoolSharesEndToEnd(t *testing.T) {
	setCalcDay(t, "2026-11-06")
	prev := saturationExponent
	saturationExponent = 1 // presence rules only
	t.Cleanup(func() { saturationExponent = prev })

	// 16 visors share one IP (each 8/16), one visor is alone.
	var nodes []nodeinfo
	ips, macs := map[string]int{}, map[string]int{}
	for i := range 16 {
		nodes = append(nodes, nodeinfo{IPAddr: "crowded", MacAddr: string(rune('a' + i)), SkyAddr: "farm"})
	}
	nodes = append(nodes, nodeinfo{IPAddr: "alone", MacAddr: "z", SkyAddr: "solo"})
	for _, n := range nodes {
		ips[n.IPAddr]++
		macs[n.MacAddr]++
	}

	computePoolShares(nodes, ips, macs)
	if total := computePoolRewards(nodes, 90); !near(total, 9) { // 16*0.5 + 1
		t.Fatalf("total shares = %v, want 9", total)
	}
	byAddr := sumRewardsByAddress(nodes)
	if byAddr[0].SkyAddr != "farm" || !near(byAddr[0].Reward, 80) || !near(byAddr[1].Reward, 10) {
		t.Errorf("payout = %+v, want farm 80 and solo 10", byAddr)
	}
}
