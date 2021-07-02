package router

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

func TestMain(m *testing.M) {
	loggingLevel, ok := os.LookupEnv("TEST_LOGGING_LEVEL")
	if ok {
		lvl, err := logging.LevelFromString(loggingLevel)
		if err != nil {
			log.Fatal(err)
		}

		logging.SetLevel(lvl)
	} else {
		logging.SetLevel(logrus.TraceLevel)
	}

	os.Exit(m.Run())
}

const ruleKeepAlive = 1 * time.Hour

func testHandlePackets(t *testing.T, r0, r1 *router, tp1 *transport.ManagedTransport, pk1, pk2 cipher.PubKey) {
	var wg sync.WaitGroup

	wg.Add(1)
	t.Run("handlePacket_fwdRule", func(t *testing.T) {
		defer wg.Done()

		testForwardRule(t, r0, r1, tp1, pk1, pk2)
	})
	wg.Wait()

	wg.Add(1)
	t.Run("handlePacket_intFwdRule", func(t *testing.T) {
		defer wg.Done()

		testIntermediaryForwardRule(t, r0, r1, tp1)
	})
	wg.Wait()

	wg.Add(1)
	t.Run("handlePacket_cnsmRule", func(t *testing.T) {
		defer wg.Done()

		testConsumeRule(t, r0, r1, tp1, pk1, pk2)
	})
	wg.Wait()

	wg.Add(1)
	t.Run("handlePacket_close_initiator", func(t *testing.T) {
		defer wg.Done()

		testClosePacketInitiator(t, r0, r1, pk1, pk2, tp1)
	})
	wg.Wait()

	wg.Add(1)
	t.Run("handlePacket_close_remote", func(t *testing.T) {
		defer wg.Done()

		testClosePacketRemote(t, r0, r1, pk1, pk2, tp1)
	})
	wg.Wait()

	wg.Add(1)
	t.Run("handlePacket_keepalive", func(t *testing.T) {
		defer wg.Done()

		testKeepAlivePacket(t, r0, r1, pk1, pk2)
	})
	wg.Wait()
}

func testKeepAlivePacket(t *testing.T, r0, r1 *router, pk1, pk2 cipher.PubKey) {
	defer clearRouterRules(r0, r1)
	defer clearRouteGroups(r0, r1)

	rtIDs, err := r0.ReserveKeys(1)
	require.NoError(t, err)

	rtID := rtIDs[0]

	cnsmRule := routing.ConsumeRule(100*time.Millisecond, rtID, pk2, pk1, 0, 0)
	err = r0.rt.SaveRule(cnsmRule)
	require.NoError(t, err)
	require.Len(t, r0.rt.AllRules(), 1)

	time.Sleep(10 * time.Millisecond)

	packet := routing.MakeKeepAlivePacket(rtIDs[0])
	require.NoError(t, r0.handleTransportPacket(context.TODO(), packet))

	require.Len(t, r0.rt.AllRules(), 1)
	time.Sleep(10 * time.Millisecond)
	require.Len(t, r0.rt.AllRules(), 1)

	time.Sleep(200 * time.Millisecond)
	require.Len(t, r0.rt.AllRules(), 0)
}

func testClosePacketRemote(t *testing.T, r0, r1 *router, pk1, pk2 cipher.PubKey, tp1 *transport.ManagedTransport) {
	defer clearRouterRules(r0, r1)
	defer clearRouteGroups(r0, r1)

	// reserve FWD IDs for r0.
	intFwdID, err := r0.ReserveKeys(1)
	require.NoError(t, err)

	// reserve FWD and CNSM IDs for r1.
	r1RtIDs, err := r1.ReserveKeys(2)
	require.NoError(t, err)

	intFwdRule := routing.IntermediaryForwardRule(1*time.Hour, intFwdID[0], r1RtIDs[1], tp1.Entry.ID)
	err = r0.rt.SaveRule(intFwdRule)
	require.NoError(t, err)

	routeID := routing.RouteID(7)
	fwdRule := routing.ForwardRule(ruleKeepAlive, r1RtIDs[0], routeID, tp1.Entry.ID, pk1, pk2, 0, 0)
	cnsmRule := routing.ConsumeRule(ruleKeepAlive, r1RtIDs[1], pk2, pk1, 0, 0)

	err = r1.rt.SaveRule(fwdRule)
	require.NoError(t, err)

	err = r1.rt.SaveRule(cnsmRule)
	require.NoError(t, err)

	fwdRtDesc := fwdRule.RouteDescriptor()

	rules := routing.EdgeRules{
		Desc:    fwdRtDesc.Invert(),
		Forward: fwdRule,
		Reverse: cnsmRule,
	}

	rg1 := NewRouteGroup(DefaultRouteGroupConfig(), r1.rt, rules.Desc)
	rg1.appendRules(rules.Forward, rules.Reverse, r1.tm.Transport(rules.Forward.NextTransportID()))

	nrg1 := &NoiseRouteGroup{rg: rg1}
	r1.rgsNs[rg1.desc] = nrg1

	packet := routing.MakeClosePacket(intFwdID[0], routing.CloseRequested)
	err = r0.handleTransportPacket(context.TODO(), packet)
	require.NoError(t, err)

	recvPacket, err := r1.tm.ReadPacket()
	require.NoError(t, err)
	require.Equal(t, packet.Size(), recvPacket.Size())
	require.Equal(t, packet.Payload(), recvPacket.Payload())
	require.Equal(t, packet.Type(), recvPacket.Type())
	require.Equal(t, r1RtIDs[1], recvPacket.RouteID())

	err = r1.handleTransportPacket(context.TODO(), recvPacket)
	require.NoError(t, err)

	require.True(t, nrg1.rg.isRemoteClosed())
	require.False(t, nrg1.isClosed())
	require.Len(t, r1.rgsNs, 0)
	require.Len(t, r0.rt.AllRules(), 0)
	require.Len(t, r1.rt.AllRules(), 0)
}

func testClosePacketInitiator(t *testing.T, r0, r1 *router, pk1, pk2 cipher.PubKey, tp1 *transport.ManagedTransport) {
	defer clearRouterRules(r0, r1)
	defer clearRouteGroups(r0, r1)

	// reserve FWD IDs for r0.
	intFwdID, err := r0.ReserveKeys(1)
	require.NoError(t, err)

	// reserve FWD and CNSM IDs for r1.
	r1RtIDs, err := r1.ReserveKeys(2)
	require.NoError(t, err)

	intFwdRule := routing.IntermediaryForwardRule(1*time.Hour, intFwdID[0], r1RtIDs[1], tp1.Entry.ID)
	err = r0.rt.SaveRule(intFwdRule)
	require.NoError(t, err)

	routeID := routing.RouteID(7)
	fwdRule := routing.ForwardRule(ruleKeepAlive, r1RtIDs[0], routeID, tp1.Entry.ID, pk1, pk2, 0, 0)
	cnsmRule := routing.ConsumeRule(ruleKeepAlive, r1RtIDs[1], pk2, pk1, 0, 0)

	err = r1.rt.SaveRule(fwdRule)
	require.NoError(t, err)

	err = r1.rt.SaveRule(cnsmRule)
	require.NoError(t, err)

	fwdRtDesc := fwdRule.RouteDescriptor()

	rules := routing.EdgeRules{
		Desc:    fwdRtDesc.Invert(),
		Forward: fwdRule,
		Reverse: cnsmRule,
	}

	rg1 := NewRouteGroup(DefaultRouteGroupConfig(), r1.rt, rules.Desc)
	rg1.appendRules(rules.Forward, rules.Reverse, r1.tm.Transport(rules.Forward.NextTransportID()))

	nrg1 := &NoiseRouteGroup{rg: rg1}
	r1.rgsNs[rg1.desc] = nrg1

	packet := routing.MakeClosePacket(intFwdID[0], routing.CloseRequested)
	err = r0.handleTransportPacket(context.TODO(), packet)
	require.NoError(t, err)

	recvPacket, err := r1.tm.ReadPacket()
	require.NoError(t, err)
	require.Equal(t, packet.Size(), recvPacket.Size())
	require.Equal(t, packet.Payload(), recvPacket.Payload())
	require.Equal(t, packet.Type(), recvPacket.Type())
	require.Equal(t, r1RtIDs[1], recvPacket.RouteID())

	rg1.closeDone.Add(1)
	rg1.closeInitiated = 1

	err = r1.handleTransportPacket(context.TODO(), recvPacket)
	require.NoError(t, err)

	require.Len(t, r1.rgsNs, 0)
	require.Len(t, r0.rt.AllRules(), 0)
	// since this is the close initiator but the close routine wasn't called,
	// forward rule is left
	require.Len(t, r1.rt.AllRules(), 1)
}

// TEST: Ensure handleTransportPacket does as expected.
// After setting a rule in r0, r0 should forward a packet to r1 (as specified in the given rule)
// when r0.handleTransportPacket() is called.
func testForwardRule(t *testing.T, r0, r1 *router, tp1 *transport.ManagedTransport, pk1, pk2 cipher.PubKey) {
	defer clearRouterRules(r0, r1)
	defer clearRouteGroups(r0, r1)

	// Add a FWD rule for r0.
	fwdRtID, err := r0.ReserveKeys(1)
	require.NoError(t, err)

	routeID := routing.RouteID(1)
	fwdRule := routing.ForwardRule(ruleKeepAlive, fwdRtID[0], routeID, tp1.Entry.ID, pk1, pk2, 0, 0)
	err = r0.rt.SaveRule(fwdRule)
	require.NoError(t, err)

	rules := routing.EdgeRules{Desc: fwdRule.RouteDescriptor(), Forward: fwdRule, Reverse: nil}
	rg0 := NewRouteGroup(DefaultRouteGroupConfig(), r0.rt, rules.Desc)
	rg0.appendRules(rules.Forward, rules.Reverse, r0.tm.Transport(rules.Forward.NextTransportID()))

	nrg0 := &NoiseRouteGroup{rg: rg0}
	r0.rgsNs[rg0.desc] = nrg0

	// Call handleTransportPacket for r0 (this should in turn, use the rule we added).
	packet, err := routing.MakeDataPacket(fwdRtID[0], []byte("This is a test!"))
	require.NoError(t, err)

	require.NoError(t, r0.handleTransportPacket(context.TODO(), packet))

	// r1 should receive the packet handled by r0.
	recvPacket, err := r1.tm.ReadPacket()
	assert.NoError(t, err)
	assert.Equal(t, packet.Size(), recvPacket.Size())
	assert.Equal(t, packet.Payload(), recvPacket.Payload())
	assert.Equal(t, routeID, recvPacket.RouteID())
}

func testIntermediaryForwardRule(t *testing.T, r0, r1 *router, tp1 *transport.ManagedTransport) {
	defer clearRouterRules(r0, r1)
	defer clearRouteGroups(r0, r1)

	// Add a FWD rule for r0.
	fwdRtID, err := r0.ReserveKeys(1)
	require.NoError(t, err)

	fwdRule := routing.IntermediaryForwardRule(ruleKeepAlive, fwdRtID[0], routing.RouteID(5), tp1.Entry.ID)
	err = r0.rt.SaveRule(fwdRule)
	require.NoError(t, err)

	// Call handleTransportPacket for r0 (this should in turn, use the rule we added).
	packet, err := routing.MakeDataPacket(fwdRtID[0], []byte("This is a test!"))
	require.NoError(t, err)

	require.NoError(t, r0.handleTransportPacket(context.TODO(), packet))

	// r1 should receive the packet handled by r0.
	recvPacket, err := r1.tm.ReadPacket()
	assert.NoError(t, err)
	assert.Equal(t, packet.Size(), recvPacket.Size())
	assert.Equal(t, packet.Payload(), recvPacket.Payload())
	assert.Equal(t, routing.RouteID(5), recvPacket.RouteID())
}

func testConsumeRule(t *testing.T, r0, r1 *router, tp1 *transport.ManagedTransport, pk1, pk2 cipher.PubKey) {
	defer clearRouterRules(r0, r1)
	defer clearRouteGroups(r0, r1)

	// one for consume rule and one for reverse forward rule
	dstRtIDs, err := r1.ReserveKeys(2)
	require.NoError(t, err)

	intFwdRtID, err := r0.ReserveKeys(1)
	require.NoError(t, err)

	intFwdRule := routing.IntermediaryForwardRule(ruleKeepAlive, intFwdRtID[0], dstRtIDs[1], tp1.Entry.ID)
	err = r0.rt.SaveRule(intFwdRule)
	require.NoError(t, err)

	routeID := routing.RouteID(7)
	fwdRule := routing.ForwardRule(ruleKeepAlive, dstRtIDs[0], routeID, tp1.Entry.ID, pk1, pk2, 0, 0)
	cnsmRule := routing.ConsumeRule(ruleKeepAlive, dstRtIDs[1], pk2, pk1, 0, 0)

	err = r1.rt.SaveRule(fwdRule)
	require.NoError(t, err)

	err = r1.rt.SaveRule(cnsmRule)
	require.NoError(t, err)

	fwdRtDesc := fwdRule.RouteDescriptor()

	rules := routing.EdgeRules{
		Desc:    fwdRtDesc.Invert(),
		Forward: fwdRule,
		Reverse: cnsmRule,
	}

	rg1 := NewRouteGroup(DefaultRouteGroupConfig(), r1.rt, rules.Desc)
	rg1.appendRules(rules.Forward, rules.Reverse, r1.tm.Transport(rules.Forward.NextTransportID()))

	nrg1 := &NoiseRouteGroup{rg: rg1}
	r1.rgsNs[rg1.desc] = nrg1

	packet, err := routing.MakeDataPacket(intFwdRtID[0], []byte("test intermediary forward"))
	require.NoError(t, err)

	require.NoError(t, r0.handleTransportPacket(context.TODO(), packet))

	recvPacket, err := r1.tm.ReadPacket()
	assert.NoError(t, err)
	assert.Equal(t, packet.Size(), recvPacket.Size())
	assert.Equal(t, packet.Payload(), recvPacket.Payload())
	assert.Equal(t, dstRtIDs[1], recvPacket.RouteID())

	consumeMsg := []byte("test_consume")
	packet, err = routing.MakeDataPacket(dstRtIDs[1], consumeMsg)
	require.NoError(t, err)

	require.NoError(t, r1.handleTransportPacket(context.TODO(), packet))

	nrg, ok := r1.noiseRouteGroup(fwdRtDesc.Invert())
	require.True(t, ok)
	require.NotNil(t, nrg)

	data := <-nrg.rg.readCh
	require.Equal(t, consumeMsg, data)
}

func testRemoveRouteDescriptor(t *testing.T, r *router, rt routing.Table) {
	clearRoutingTableRules(rt)

	localPK, _ := cipher.GenerateKeyPair()
	remotePK, _ := cipher.GenerateKeyPair()

	id, err := r.rt.ReserveKeys(1)
	require.NoError(t, err)

	rule := routing.ConsumeRule(10*time.Minute, id[0], localPK, remotePK, 2, 3)
	err = r.rt.SaveRule(rule)
	require.NoError(t, err)

	desc := routing.NewRouteDescriptor(localPK, remotePK, 3, 2)
	r.RemoveRouteDescriptor(desc)
	assert.Equal(t, 1, rt.Count())

	desc = routing.NewRouteDescriptor(localPK, remotePK, 2, 3)
	r.RemoveRouteDescriptor(desc)
	assert.Equal(t, 0, rt.Count())
}

func testGetRule(t *testing.T, r *router, rt routing.Table) {
	clearRoutingTableRules(rt)

	expiredID, err := r.rt.ReserveKeys(1)
	require.NoError(t, err)

	expiredRule := routing.IntermediaryForwardRule(-10*time.Minute, expiredID[0], 3, uuid.New())
	err = r.rt.SaveRule(expiredRule)
	require.NoError(t, err)

	id, err := r.rt.ReserveKeys(1)
	require.NoError(t, err)

	rule := routing.IntermediaryForwardRule(10*time.Minute, id[0], 3, uuid.New())
	err = r.rt.SaveRule(rule)
	require.NoError(t, err)

	defer r.rt.DelRules([]routing.RouteID{id[0], expiredID[0]})

	// rule should already be expired at this point due to the execution time.
	// However, we'll just a bit to be sure
	time.Sleep(1 * time.Millisecond)

	_, err = r.GetRule(expiredID[0])
	require.Error(t, err)

	_, err = r.GetRule(123)
	require.Error(t, err)

	gotRule, err := r.GetRule(id[0])
	require.NoError(t, err)
	assert.Equal(t, rule, gotRule)
}

func clearRouteGroups(routers ...*router) {
	for _, r := range routers {
		r.rgsNs = make(map[routing.RouteDescriptor]*NoiseRouteGroup)
	}
}

func clearRouterRules(routers ...*router) {
	for _, r := range routers {
		rules := r.rt.AllRules()
		for _, rule := range rules {
			r.rt.DelRules([]routing.RouteID{rule.KeyRouteID()})
		}
	}
}

func clearRoutingTableRules(rt routing.Table) {
	rules := rt.AllRules()
	for _, rule := range rules {
		rt.DelRules([]routing.RouteID{rule.KeyRouteID()})
	}
}

type TestEnv struct {
	TpD transport.DiscoveryClient

	TpMngrConfs []*transport.ManagerConfig
	TpMngrs     []*transport.Manager

	teardown func()
}

func (e *TestEnv) GenRouterConfig(i int) *Config {
	return &Config{
		Logger:           logging.MustGetLogger(fmt.Sprintf("router_%d", i)),
		PubKey:           e.TpMngrConfs[i].PubKey,
		SecKey:           e.TpMngrConfs[i].SecKey,
		TransportManager: e.TpMngrs[i],
		SetupNodes:       nil, // TODO
	}
}

func (e *TestEnv) Teardown() {
	e.teardown()
}
