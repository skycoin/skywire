package visor

import (
	"errors"
	"strconv"
	"testing"
	"time"

	rpc "github.com/0magnet/gobrpc"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/internal/rpctest"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// rpcTestAPI answers the few visor calls the registration test makes.
type rpcTestAPI struct {
	visorapi.API
	tpsNodes []cipher.PubKey
}

func (a rpcTestAPI) GetTransportSetupNodes() ([]cipher.PubKey, error) { return a.tpsNodes, nil }

func (a rpcTestAPI) SetRewardAddress(p string) (string, error) {
	if p == "" {
		return "", errors.New("empty reward address")
	}
	return "stored:" + p, nil
}

func (a rpcTestAPI) UptimeHistory(args visorapi.UptimeHistoryArgs) (*visorapi.UptimeHistoryResponse, error) {
	return &visorapi.UptimeHistoryResponse{TimelineDate: args.Since.UTC().Format(time.DateOnly), Timeline: []byte(strconv.Itoa(args.Limit))}, nil
}

func TestVisorRPCHandlers(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	r := &RPC{visor: rpcTestAPI{tpsNodes: []cipher.PubKey{pk}}, log: logrus.New()}
	reg := func(s *rpc.Server, name string) error { return registerVisorRPC(s, name, r) }

	rpctest.CheckHandlers(t, r, reg)

	got := rpctest.CompareCall(t, r, reg, "GetTransportSetupNodes", &struct{}{}, func() any { return new([]cipher.PubKey) })
	if nodes := *got.(*[]cipher.PubKey); len(nodes) != 1 || nodes[0] != pk {
		t.Errorf("GetTransportSetupNodes = %v", nodes)
	}
	got = rpctest.CompareCall(t, r, reg, "SetRewardAddress", "addr", func() any { return new(string) })
	if s := *got.(*string); s != "stored:addr" {
		t.Errorf("SetRewardAddress = %q", s)
	}
	rpctest.CompareCall(t, r, reg, "SetRewardAddress", "", func() any { return new(string) })
	since := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	got = rpctest.CompareCall(t, r, reg, "UptimeHistory", &visorapi.UptimeHistoryArgs{Since: since, Limit: 3}, func() any { return new(visorapi.UptimeHistoryResponse) })
	if h := got.(*visorapi.UptimeHistoryResponse); h.TimelineDate != "2026-10-01" || string(h.Timeline) != "3" {
		t.Errorf("UptimeHistory = %+v", h)
	}
}

func TestTPSRPCHandlers(t *testing.T) {
	gw := &TPSRPCGateway{}
	rpctest.CheckHandlers(t, gw, func(s *rpc.Server, name string) error { return registerTPSRPCGateway(s, name, gw) })
}
