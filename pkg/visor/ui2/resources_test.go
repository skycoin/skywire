package ui2

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHostUptime(t *testing.T) {
	require.Equal(t, "13d 7h", HostUptime(13*86400+7*3600+59))
	require.Equal(t, "2h 5m", HostUptime(2*3600+5*60))
}
