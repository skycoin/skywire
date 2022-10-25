// Package router pkg/router/route_group_test.go
package router

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

func TestNewRouteGroup(t *testing.T) {
	rg := createRouteGroup(DefaultRouteGroupConfig())
	require.NotNil(t, rg)
	require.Equal(t, DefaultRouteGroupConfig(), rg.cfg)
}

func TestRouteGroup_LocalAddr(t *testing.T) {
	rg := createRouteGroup(DefaultRouteGroupConfig())
	require.Equal(t, rg.desc.Dst(), rg.LocalAddr())

	require.NoError(t, rg.Close())
}

func TestRouteGroup_RemoteAddr(t *testing.T) {
	rg := createRouteGroup(DefaultRouteGroupConfig())
	require.Equal(t, rg.desc.Src(), rg.RemoteAddr())

	require.NoError(t, rg.Close())
}

func createRouteGroup(cfg *RouteGroupConfig) *RouteGroup {
	l := logging.NewMasterLogger()
	rt := routing.NewTable(l.PackageLogger("rgt"))

	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	port1 := routing.Port(1)
	port2 := routing.Port(2)
	desc := routing.NewRouteDescriptor(pk1, pk2, port1, port2)

	rg := NewRouteGroup(cfg, rt, desc, l)
	return rg
}
