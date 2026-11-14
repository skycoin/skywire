package ui2

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRewardsListRender(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	dates := LastDays(now, 7)
	require.Equal(t, "2026-11-11", dates[0])
	require.Equal(t, "2026-11-05", dates[6])
	l := RewardsList{Dates: dates, Rows: []RewardRow{{
		Node:  Node{PK: "02ab", Online: true, Reward: "addr1"},
		Daily: map[string]RewardDay{"2026-11-10": {Amount: 1.5, Sent: true}},
		Total: 1.5,
	}}}
	out := renderEN(t, RewardsListPage(l))
	require.Contains(t, out, "Nov 10")
	require.Contains(t, out, `<span class="ok" title="">1.500</span>`)
	require.Contains(t, out, "addr1")
}
