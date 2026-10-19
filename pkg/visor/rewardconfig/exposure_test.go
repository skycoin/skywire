package rewardconfig

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseAddressUxouts(t *testing.T) {
	const addr = "2VfPAT8tZMwoypWSZgdhgCiKrYdYoSxy1dE"

	e, err := ParseAddressUxouts(addr, []byte(`[{"coins":1000000,"spent_block_seq":0},{"coins":5000000,"spent_block_seq":0}]`))
	require.NoError(t, err)
	require.False(t, e.Spent)
	require.Equal(t, "6", e.Balance)
	require.Empty(t, e.Warning())

	e, err = ParseAddressUxouts(addr, []byte(`[{"coins":2236585000,"spent_block_seq":0},{"coins":7000000,"spent_block_seq":195400}]`))
	require.NoError(t, err)
	require.True(t, e.Spent)
	require.Equal(t, "2236.585", e.Balance)
	require.True(t, strings.Contains(e.Warning(), "It holds 2236.585 SKY"))

	e, err = ParseAddressUxouts(addr, []byte(`[{"coins":7000000,"spent_block_seq":195400}]`))
	require.NoError(t, err)
	require.True(t, e.Spent)
	require.Equal(t, "0", e.Balance)
	require.NotContains(t, e.Warning(), "It holds")

	_, err = ParseAddressUxouts(addr, []byte(`{"error":"x"}`))
	require.Error(t, err)
}
