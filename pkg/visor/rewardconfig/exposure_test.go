package rewardconfig

import (
	"net/http"
	"net/http/httptest"
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

func TestFetchAddressExposure(t *testing.T) {
	const addr = "2VfPAT8tZMwoypWSZgdhgCiKrYdYoSxy1dE"
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/address_uxouts" || r.URL.Query().Get("address") != addr {
			http.Error(w, "bad query", http.StatusBadRequest)
			return
		}
		w.Write([]byte(`[{"coins":7000000,"spent_block_seq":195400}]`)) //nolint:errcheck,gosec
	}))
	defer node.Close()

	e, err := FetchAddressExposure(node.Client(), node.URL+"/", addr)
	require.NoError(t, err)
	require.True(t, e.Spent)
	require.Contains(t, e.Warning(), "no longer earns rewards")

	_, err = FetchAddressExposure(node.Client(), node.URL, "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6")
	require.ErrorContains(t, err, "returned 400")
}
