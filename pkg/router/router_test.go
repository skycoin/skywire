package router

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkycoinProject/skywire-mainnet/pkg/routefinder/rfclient"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
	"github.com/SkycoinProject/skywire-mainnet/pkg/setup/setupclient"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/snettest"
	"github.com/SkycoinProject/skywire-mainnet/pkg/transport"
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

func Test_router_DialRoutes(t *testing.T) {
	// We are generating two key pairs - one for the a `Router`, the other to send packets to `Router`.
	keys := snettest.GenKeyPairs(3)

	// create test env
	nEnv := snettest.NewEnv(t, keys, []string{dmsg.Type})
	defer nEnv.Teardown()

	rEnv := NewTestEnv(t, nEnv.Nets)
	defer rEnv.Teardown()

	// Create routers
	r0Ifc, err := New(nEnv.Nets[0], rEnv.GenRouterConfig(0))
	require.NoError(t, err)

	r0, ok := r0Ifc.(*router)
	require.True(t, ok)

	r1Ifc, err := New(nEnv.Nets[1], rEnv.GenRouterConfig(1))
	require.NoError(t, err)

	r1, ok := r1Ifc.(*router)
	require.True(t, ok)

	r0.conf.RouteGroupDialer = setupclient.NewMockDialer()
	r1.conf.RouteGroupDialer = setupclient.NewMockDialer()

	// prepare route group creation (client_1 will use this to request route group creation with setup node).
	desc := routing.NewRouteDescriptor(r0.conf.PubKey, r1.conf.PubKey, 1, 1)

	forwardHops := []routing.Hop{
		{From: r0.conf.PubKey, To: r1.conf.PubKey, TpID: uuid.New()},
	}

	reverseHops := []routing.Hop{
		{From: r1.conf.PubKey, To: r0.conf.PubKey, TpID: uuid.New()},
	}

	route := routing.BidirectionalRoute{
		Desc:      desc,
		KeepAlive: 1 * time.Hour,
		Forward:   forwardHops,
		Reverse:   reverseHops,
	}

	ctx := context.Background()
	testLogger := logging.MustGetLogger("setupclient_test")
	pks := []cipher.PubKey{r1.conf.PubKey}

	_, err = r0.conf.RouteGroupDialer.Dial(ctx, testLogger, nEnv.Nets[2], pks, route)
	require.NoError(t, err)
	rg, err := r0.DialRoutes(context.Background(), r0.conf.PubKey, 0, 0, nil)

	require.NoError(t, err)
	require.NotNil(t, rg)
}

func Test_router_Introduce_AcceptRoutes(t *testing.T) {
	// We are generating two key pairs - one for the a `Router`, the other to send packets to `Router`.
	keys := snettest.GenKeyPairs(2)

	// create test env
	nEnv := snettest.NewEnv(t, keys, []string{dmsg.Type})
	defer nEnv.Teardown()

	rEnv := NewTestEnv(t, nEnv.Nets)
	defer rEnv.Teardown()

	// Create routers
	r0Ifc, err := New(nEnv.Nets[0], rEnv.GenRouterConfig(0))
	require.NoError(t, err)

	r0, ok := r0Ifc.(*router)
	require.True(t, ok)

	srcPK, _ := cipher.GenerateKeyPair()
	dstPK, _ := cipher.GenerateKeyPair()

	var srcPort, dstPort routing.Port = 1, 2

	desc := routing.NewRouteDescriptor(srcPK, dstPK, srcPort, dstPort)

	dstRtIDs, err := r0.ReserveKeys(2)
	require.NoError(t, err)

	fwdRule := routing.ForwardRule(1*time.Hour, dstRtIDs[0], routing.RouteID(3), uuid.UUID{}, keys[0].PK, keys[1].PK, 4, 5)
	cnsmRule := routing.ConsumeRule(1*time.Hour, dstRtIDs[1], keys[1].PK, keys[0].PK, 5, 4)

	rules := routing.EdgeRules{
		Desc:    desc,
		Forward: fwdRule,
		Reverse: cnsmRule,
	}

	require.NoError(t, r0.IntroduceRules(rules))

	rg, err := r0.AcceptRoutes(context.Background())
	require.NoError(t, err)
	require.NotNil(t, rg)
	rg.mu.Lock()
	require.Equal(t, desc, rg.desc)
	require.Equal(t, []routing.Rule{fwdRule}, rg.fwd)
	require.Equal(t, []routing.Rule{cnsmRule}, rg.rvs)
	require.Len(t, rg.tps, 1)
	rg.mu.Unlock()

	allRules := rg.rt.AllRules()
	require.Len(t, allRules, 2)
	require.Contains(t, allRules, fwdRule)
	require.Contains(t, allRules, cnsmRule)

	require.NoError(t, r0.Close())
	require.Equal(t, io.ErrClosedPipe, r0.IntroduceRules(rules))
}

func TestRouter_Serve(t *testing.T) {
	// We are generating two key pairs - one for the a `Router`, the other to send packets to `Router`.
	keys := snettest.GenKeyPairs(2)

	// create test env
	nEnv := snettest.NewEnv(t, keys, []string{dmsg.Type})
	defer nEnv.Teardown()

	rEnv := NewTestEnv(t, nEnv.Nets)
	defer rEnv.Teardown()

	// Create routers
	r0Ifc, err := New(nEnv.Nets[0], rEnv.GenRouterConfig(0))
	require.NoError(t, err)

	r0, ok := r0Ifc.(*router)
	require.True(t, ok)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, r0.tm.Close())
	require.NoError(t, r0.Serve(ctx))
}

const ruleKeepAlive = 1 * time.Hour

// Ensure that received packets are handled properly in `(*Router).handleTransportPacket()`.
func TestRouter_handleTransportPacket(t *testing.T) {
	// We are generating two key pairs - one for the a `Router`, the other to send packets to `Router`.
	keys := snettest.GenKeyPairs(2)

	pk1 := keys[0].PK
	pk2 := keys[1].PK

	// create test env
	nEnv := snettest.NewEnv(t, keys, []string{dmsg.Type})
	defer nEnv.Teardown()

	rEnv := NewTestEnv(t, nEnv.Nets)
	defer rEnv.Teardown()

	// Create routers
	r0Ifc, err := New(nEnv.Nets[0], rEnv.GenRouterConfig(0))
	require.NoError(t, err)

	r0, ok := r0Ifc.(*router)
	require.True(t, ok)

	r1Ifc, err := New(nEnv.Nets[1], rEnv.GenRouterConfig(1))
	require.NoError(t, err)

	r1, ok := r1Ifc.(*router)
	require.True(t, ok)

	defer func() {
		require.NoError(t, r0.Close())
		require.NoError(t, r1.Close())
	}()

	// Create dmsg transport between two `snet.Network` entities.
	tp1, err := rEnv.TpMngrs[1].SaveTransport(context.TODO(), pk1, dmsg.Type)
	require.NoError(t, err)

	testHandlePackets(t, r0, r1, tp1, pk1, pk2)
}

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

	time.Sleep(50 * time.Millisecond)

	packet := routing.MakeKeepAlivePacket(rtIDs[0])
	require.NoError(t, r0.handleTransportPacket(context.TODO(), packet))

	require.Len(t, r0.rt.AllRules(), 1)
	time.Sleep(50 * time.Millisecond)
	require.Len(t, r0.rt.AllRules(), 1)

	time.Sleep(100 * time.Millisecond)
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

	rg1 := r1.saveRouteGroupRules(routing.EdgeRules{
		Desc:    fwdRtDesc.Invert(),
		Forward: fwdRule,
		Reverse: cnsmRule,
	})

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

	require.True(t, rg1.isRemoteClosed())
	require.False(t, rg1.isClosed())
	require.Len(t, r1.rgs, 0)
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

	rg1 := r1.saveRouteGroupRules(routing.EdgeRules{
		Desc:    fwdRtDesc.Invert(),
		Forward: fwdRule,
		Reverse: cnsmRule,
	})

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

	require.Len(t, r1.rgs, 0)
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
	r0.saveRouteGroupRules(routing.EdgeRules{Desc: fwdRule.RouteDescriptor(), Forward: fwdRule, Reverse: nil})

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

	r1.saveRouteGroupRules(routing.EdgeRules{
		Desc:    fwdRtDesc.Invert(),
		Forward: fwdRule,
		Reverse: cnsmRule,
	})

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

	rg, ok := r1.routeGroup(fwdRtDesc.Invert())
	require.True(t, ok)
	require.NotNil(t, rg)

	data := <-rg.readCh
	require.Equal(t, consumeMsg, data)
}

func clearRouteGroups(routers ...*router) {
	for _, r := range routers {
		r.rgs = make(map[routing.RouteDescriptor]*RouteGroup)
	}
}

func TestRouter_Rules(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()

	env := snettest.NewEnv(t, []snettest.KeyPair{{PK: pk, SK: sk}}, []string{dmsg.Type})
	defer env.Teardown()

	rt := routing.NewTable()

	// We are generating two key pairs - one for the a `Router`, the other to send packets to `Router`.
	keys := snettest.GenKeyPairs(2)

	// create test env
	nEnv := snettest.NewEnv(t, keys, []string{dmsg.Type})
	defer nEnv.Teardown()

	rEnv := NewTestEnv(t, nEnv.Nets)
	defer rEnv.Teardown()

	rIfc, err := New(nEnv.Nets[0], rEnv.GenRouterConfig(0))
	require.NoError(t, err)

	r, ok := rIfc.(*router)
	require.True(t, ok)

	defer func() {
		require.NoError(t, r.Close())
	}()

	r.rt = rt

	// TEST: Set and get expired and unexpired rule.
	t.Run("GetRule", func(t *testing.T) {
		testGetRule(t, r, rt)
	})

	// TEST: Ensure removing route descriptor works properly.
	t.Run("RemoveRouteDescriptor", func(t *testing.T) {
		testRemoveRouteDescriptor(t, r, rt)
	})
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

func TestRouter_SetupIsTrusted(t *testing.T) {
	keys := snettest.GenKeyPairs(2)

	nEnv := snettest.NewEnv(t, keys, []string{dmsg.Type})
	defer nEnv.Teardown()

	rEnv := NewTestEnv(t, nEnv.Nets)
	defer rEnv.Teardown()

	routerConfig := rEnv.GenRouterConfig(0)
	routerConfig.SetupNodes = append(routerConfig.SetupNodes, keys[0].PK)

	r0, err := New(nEnv.Nets[0], routerConfig)
	require.NoError(t, err)

	assert.True(t, r0.SetupIsTrusted(keys[0].PK))
	assert.False(t, r0.SetupIsTrusted(keys[1].PK))
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

func NewTestEnv(t *testing.T, nets []*snet.Network) *TestEnv {
	tpD := transport.NewDiscoveryMock()

	mConfs := make([]*transport.ManagerConfig, len(nets))
	ms := make([]*transport.Manager, len(nets))

	for i, n := range nets {
		var err error

		mConfs[i] = &transport.ManagerConfig{
			PubKey:          n.LocalPK(),
			SecKey:          n.LocalSK(),
			DiscoveryClient: tpD,
			LogStore:        transport.InMemoryTransportLogStore(),
		}

		ms[i], err = transport.NewManager(nil, n, mConfs[i])
		require.NoError(t, err)

		go ms[i].Serve(context.TODO())
	}

	teardown := func() {
		for _, m := range ms {
			assert.NoError(t, m.Close())
		}
	}

	return &TestEnv{
		TpD:         tpD,
		TpMngrConfs: mConfs,
		TpMngrs:     ms,
		teardown:    teardown,
	}
}

func (e *TestEnv) GenRouterConfig(i int) *Config {
	return &Config{
		Logger:           logging.MustGetLogger(fmt.Sprintf("router_%d", i)),
		PubKey:           e.TpMngrConfs[i].PubKey,
		SecKey:           e.TpMngrConfs[i].SecKey,
		TransportManager: e.TpMngrs[i],
		RouteFinder:      rfclient.NewMock(),
		SetupNodes:       nil, // TODO
	}
}

func (e *TestEnv) Teardown() {
	e.teardown()
}
