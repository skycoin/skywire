package pty

import (
	"testing"

	rpc "github.com/0magnet/gobrpc"

	"github.com/skycoin/skywire/internal/rpctest"
	"github.com/skycoin/skywire/pkg/cipher"
)

func TestPtyRPCHandlers(t *testing.T) {
	wl := NewWhitelistGateway(NewMemoryWhitelist())
	reg := func(s *rpc.Server, name string) error { return registerWhitelistGateway(s, name, wl) }
	rpctest.CheckHandlers(t, wl, reg)
	pk, _ := cipher.GenerateKeyPair()
	add := []cipher.PubKey{pk}
	rpctest.CompareCall(t, wl, reg, "WhitelistAdd", &add, func() any { return new(struct{}) })
	got := rpctest.CompareCall(t, wl, reg, "Whitelist", &struct{}{}, func() any { return new([]cipher.PubKey) })
	if pks := *got.(*[]cipher.PubKey); len(pks) != 1 || pks[0] != pk {
		t.Errorf("Whitelist = %v", pks)
	}

	local := &LocalPtyGateway{}
	rpctest.CheckHandlers(t, local, func(s *rpc.Server, name string) error { return registerLocalPtyGateway(s, name, local) })
	proxied := &ProxiedPtyGateway{}
	rpctest.CheckHandlers(t, proxied, func(s *rpc.Server, name string) error { return registerProxiedPtyGateway(s, name, proxied) })
	session := &sessionPtyGateway{}
	rpctest.CheckHandlers(t, session, func(s *rpc.Server, name string) error { return registerSessionPtyGateway(s, name, session) })
}
