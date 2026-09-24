// Package router pkg/router/rule_ingress_test.go
package router

import (
	"context"
	"net"
	"net/rpc"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// Well-formed rules for tests that only need something to pass Validate.
var (
	testFwdRule    = routing.ForwardRule(time.Minute, 1, 2, uuid.New(), cipher.PubKey{}, cipher.PubKey{}, 3, 4)
	testRevRule    = routing.ConsumeRule(time.Minute, 5, cipher.PubKey{}, cipher.PubKey{}, 3, 4)
	testInterRule  = routing.IntermediaryForwardRule(time.Minute, 6, 7, uuid.New())
	testInterRule2 = routing.IntermediaryForwardRule(time.Minute, 8, 9, uuid.New())
)

// malformedRules are rules a setup node, RSN or managed visor could send that
// no accessor may be handed: truncated for their type, or of an unknown type.
func malformedRules() map[string]routing.Rule {
	unknown := append(routing.Rule(nil), testFwdRule...)
	unknown[8] = 7
	return map[string]routing.Rule{
		"short header":       make(routing.Rule, routing.RuleHeaderSize-1),
		"truncated forward":  testFwdRule[:routing.RuleHeaderSize+70+4+15],
		"truncated consume":  testRevRule[:routing.RuleHeaderSize+69],
		"truncated interfwd": testInterRule[:routing.RuleHeaderSize+4+15],
		"unknown type":       unknown,
	}
}

// AddEdgeRules and AddIntermediaryRules answer a malformed rule with a
// failure, never reach the router with it, and never panic. Each is driven
// over a real RPC connection, so the rules cross the same gob decode a setup
// node's request does.
func TestRPCGateway_RejectsMalformedRules(t *testing.T) {
	for name, bad := range malformedRules() {
		t.Run(name, func(t *testing.T) {
			r := &MockRouter{}
			_, cl, cleanup := prepRPCServerAndClient(t, r)
			defer cleanup()
			ctx := context.Background()

			for _, edge := range []routing.EdgeRules{
				{Forward: bad, Reverse: testRevRule},
				{Forward: testFwdRule, Reverse: bad},
			} {
				ok, err := cl.AddEdgeRules(ctx, edge)
				require.Error(t, err)
				require.False(t, ok)
			}
			ok, err := cl.AddIntermediaryRules(ctx, []routing.Rule{testInterRule, bad})
			require.Error(t, err)
			require.False(t, ok)

			r.AssertNotCalled(t, "IntroduceRules")
			r.AssertNotCalled(t, "SaveRoutingRules")
		})
	}

	// Rules of the wrong role are as unusable as short ones.
	r := &MockRouter{}
	var ok bool
	err := NewRPCGateway(r, mlog, false).AddEdgeRules(routing.EdgeRules{Forward: testRevRule, Reverse: testFwdRule}, &ok)
	require.ErrorContains(t, err, routing.ErrInvalidRule.Error())
	r.AssertNotCalled(t, "IntroduceRules")
}

// A cascade install (RSN-signed) whose rule data is malformed is refused with
// an error before any rule is saved or any route group is introduced.
func TestCascadeInstall_RejectsMalformedRules(t *testing.T) {
	desc := routing.NewRouteDescriptor(cipher.PubKey{1}, cipher.PubKey{2}, 3, 4)
	for name, bad := range malformedRules() {
		for _, edge := range []bool{false, true} {
			rt := routing.NewTable(logging.MustGetLogger("test_rt"))
			introduced := false
			ch := NewCascadeHandler(logging.MustGetLogger("test_cascade"), cipher.PubKey{}, nil, rt, nil,
				func(routing.EdgeRules) error { introduced = true; return nil })

			msg := &routing.CascadeSetup{Phase: routing.CascadePhaseInstall}
			rules := []routing.Rule{testInterRule, bad}
			if edge {
				msg.EdgeDesc = desc
				rules = []routing.Rule{testFwdRule, bad}
			}
			msg.RuleData = routing.SerializeRules(rules)

			var err error
			require.NotPanics(t, func() { _, err = ch.processInstall(msg) }, name)
			require.ErrorIs(t, err, routing.ErrInvalidRule, name)
			require.False(t, introduced, name)
			require.Zero(t, rt.Count(), name)
		}
	}

	// Well-formed rules in the wrong roles cannot become a route group either.
	rt := routing.NewTable(logging.MustGetLogger("test_rt"))
	ch := NewCascadeHandler(logging.MustGetLogger("test_cascade"), cipher.PubKey{}, nil, rt, nil,
		func(routing.EdgeRules) error { t.Fatal("introduced swapped edge rules"); return nil })
	_, err := ch.processInstall(&routing.CascadeSetup{
		Phase:    routing.CascadePhaseInstall,
		EdgeDesc: desc,
		RuleData: routing.SerializeRules([]routing.Rule{testRevRule, testFwdRule}),
	})
	require.ErrorIs(t, err, routing.ErrInvalidRule)
}

// fakeSetupNode answers route setup with whatever rules it was given.
type fakeSetupNode struct{ rules routing.EdgeRules }

func (f *fakeSetupNode) DialRouteGroup(_ routing.BidirectionalRoute, rules *routing.EdgeRules) error {
	*rules = f.rules
	return nil
}

func (f *fakeSetupNode) DialRouteGroupBatch(batch *routing.BidirectionalRouteBatch, reply *routing.BidirectionalRouteBatchReply) error {
	for _, r := range batch.Routes {
		reply.Results = append(reply.Results, routing.BatchRouteResult{ID: r.ID, Rules: f.rules})
	}
	return nil
}

func fakeSetupClient(t *testing.T, rules routing.EdgeRules) *SetupClient {
	t.Helper()
	l, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err)
	s := rpc.NewServer()
	require.NoError(t, s.RegisterName(rpcName, &fakeSetupNode{rules: rules}))
	go s.Accept(l)
	conn, err := net.Dial("tcp", l.Addr().String())
	require.NoError(t, err)
	c := &SetupClient{rpc: rpc.NewClient(conn)}
	t.Cleanup(func() {
		c.rpc.Close() //nolint:errcheck,gosec
		l.Close()     //nolint:errcheck,gosec
	})
	return c
}

// A setup node's reply becomes the dialing visor's route group, so a malformed
// rule in it fails the dial (or, in a batch, that one member).
func TestSetupClient_RejectsMalformedReply(t *testing.T) {
	ctx := context.Background()
	for name, bad := range malformedRules() {
		t.Run(name, func(t *testing.T) {
			c := fakeSetupClient(t, routing.EdgeRules{Forward: testFwdRule, Reverse: bad})

			rules, err := c.DialRouteGroup(ctx, routing.BidirectionalRoute{})
			require.ErrorIs(t, err, routing.ErrInvalidRule)
			require.Equal(t, routing.EdgeRules{}, rules)

			reply, err := c.DialRouteGroupBatch(ctx, routing.BidirectionalRouteBatch{
				Routes: []routing.BatchRouteRequest{{ID: 0}},
			})
			require.NoError(t, err)
			require.Len(t, reply.Results, 1)
			require.True(t, reply.Results[0].Failed())
			require.Contains(t, reply.Results[0].Error, routing.ErrInvalidRule.Error())
		})
	}

	good := routing.EdgeRules{Forward: testFwdRule, Reverse: testRevRule}
	c := fakeSetupClient(t, good)
	rules, err := c.DialRouteGroup(ctx, routing.BidirectionalRoute{})
	require.NoError(t, err)
	require.Equal(t, good, rules)
}
