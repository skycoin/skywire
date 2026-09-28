package servicedisc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAddrKeepsPathPrefix(t *testing.T) {
	c := &HTTPClient{conf: Config{DiscAddr: "dmsg://0300000000000000000000000000000000000000000000000000000000000000ab:80/sd"}}
	got, err := c.addr("/api/services", "proxy", "", "", 0)
	require.NoError(t, err)
	require.Equal(t, "dmsg://0300000000000000000000000000000000000000000000000000000000000000ab:80/sd/api/services?type=proxy", got)

	c = &HTTPClient{conf: Config{DiscAddr: "http://sd.example.com/"}}
	got, err = c.addr("/api/services", "", "", "", 0)
	require.NoError(t, err)
	require.Equal(t, "http://sd.example.com/api/services", got)
}
