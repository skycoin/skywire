package routing

import (
	"testing"

	"github.com/skycoin/dmsg/cipher"
	"github.com/stretchr/testify/require"
)

func TestRouteDescriptor(t *testing.T) {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	port1 := Port(1)
	port2 := Port(2)

	rd := NewRouteDescriptor(pk1, pk2, port1, port2)

	require.Len(t, rd, routeDescriptorSize)
	require.Equal(t, pk1, rd.SrcPK())
	require.Equal(t, pk2, rd.DstPK())
	require.Equal(t, port1, rd.SrcPort())
	require.Equal(t, port2, rd.DstPort())

	inverted := rd.Invert()

	require.Len(t, inverted, routeDescriptorSize)
	require.Equal(t, pk2, inverted.SrcPK())
	require.Equal(t, pk1, inverted.DstPK())
	require.Equal(t, port2, inverted.SrcPort())
	require.Equal(t, port1, inverted.DstPort())
}
