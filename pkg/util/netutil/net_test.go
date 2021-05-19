package netutil_test

import (
	"github.com/skycoin/skywire/pkg/util/netutil"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDefaultNetworkInterfaceIPs(t *testing.T) {
	req := require.New(t)

	ifaceIPs, err := netutil.DefaultNetworkInterfaceIPs()
	req.NoError(err)
	t.Logf("interface IP: %v", ifaceIPs)
}
