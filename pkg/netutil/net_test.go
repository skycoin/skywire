// Package netutil pkg/netutil/net_test.go
package netutil_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/netutil"
)

func TestDefaultNetworkInterfaceIPs(t *testing.T) {
	req := require.New(t)

	ifaceIPs, err := netutil.DefaultNetworkInterfaceIPs()
	req.NoError(err)
	t.Logf("interface IP: %v", ifaceIPs)
}
