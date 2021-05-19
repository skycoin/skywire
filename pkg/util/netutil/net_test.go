package netutil_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/util/netutil"
)

func TestDefaultNetworkInterfaceIPs(t *testing.T) {
	req := require.New(t)

	ifaceIPs, err := netutil.DefaultNetworkInterfaceIPs()
	req.NoError(err)
	t.Logf("interface IP: %v", ifaceIPs)
}
