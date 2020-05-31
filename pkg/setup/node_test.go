package setup

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"testing"
	"time"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
)

func TestGenerateRules(t *testing.T) {
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	pkC, _ := cipher.GenerateKeyPair()
	pkD, _ := cipher.GenerateKeyPair()

	type testCase struct {
		fwd routing.Route
		rev routing.Route
	}

	testCases := []testCase{
		{
			fwd: routing.Route{
				Desc: routing.NewRouteDescriptor(pkA, pkC, 1, 0),
				Hops: []routing.Hop{
					{TpID: uuid.New(), From: pkA, To: pkB},
					{TpID: uuid.New(), From: pkB, To: pkD},
					{TpID: uuid.New(), From: pkD, To: pkC},
				},
			},
			rev: routing.Route{
				Desc: routing.NewRouteDescriptor(pkC, pkA, 0, 1),
				Hops: []routing.Hop{
					{TpID: uuid.New(), From: pkC, To: pkB},
					{TpID: uuid.New(), From: pkB, To: pkA},
				},
			},
		},
	}

	for i, tc := range testCases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			// arrange
			rtIDR := newMockReserver(t, nil)

			// act
			fwd, rev, inter, err1 := GenerateRules(rtIDR, []routing.Route{tc.fwd, tc.rev})
			t.Log("FORWARD:", fwd)
			t.Log("REVERSE:", rev)
			t.Log("INTERMEDIARY:", inter)

			// assert
			// TODO: We need more checks here
			require.NoError(t, err1)
			require.Len(t, fwd, 2)
			require.Len(t, rev, 2)
		})
	}
}

func TestBroadcastIntermediaryRules(t *testing.T) {
	const ctxTimeout = time.Second
	const failingTimeout = time.Second * 5

	type testCase struct {
		workingRouters int // number of working routers
		failingRouters int // number of failing routers
	}

	testCases := []testCase{
		{workingRouters: 4, failingRouters: 0},
		{workingRouters: 12, failingRouters: 1},
		{workingRouters: 9, failingRouters: 2},
		{workingRouters: 0, failingRouters: 3},
	}

	for _, tc := range testCases {
		name := fmt.Sprintf("%d_normal_%d_failing", tc.workingRouters, tc.failingRouters)

		t.Run(name, func(t *testing.T) {
			// arrange
			workingPKs := randPKs(tc.workingRouters)
			failingPKs := randPKs(tc.failingRouters)

			gateways := make(map[cipher.PubKey]*mockGatewayForReserver, tc.workingRouters+tc.failingRouters)
			for _, pk := range workingPKs {
				gateways[pk] = &mockGatewayForReserver{}
			}
			for _, pk := range failingPKs {
				gateways[pk] = &mockGatewayForReserver{hangDuration: failingTimeout}
			}

			rtIDR := newMockReserver(t, gateways)
			rules := randRulesMap(append(workingPKs, failingPKs...))

			ctx, cancel := context.WithDeadline(context.TODO(), time.Now().Add(ctxTimeout))
			defer cancel()

			// act
			err := BroadcastIntermediaryRules(ctx, logrus.New(), rtIDR, rules)

			// assert
			if tc.failingRouters > 0 {
				assert.EqualError(t, err, context.DeadlineExceeded.Error())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func randPKs(n int) []cipher.PubKey {
	out := make([]cipher.PubKey, n)
	for i := range out {
		out[i], _ = cipher.GenerateKeyPair()
	}
	return out
}

func randRulesMap(pks []cipher.PubKey) RulesMap {
	rules := make(RulesMap, len(pks))
	for _, pk := range pks {
		rules[pk] = randIntermediaryRules(2)
	}
	return rules
}

func randIntermediaryRules(n int) []routing.Rule {
	const keepAlive = time.Second
	randRtID := func() routing.RouteID { return routing.RouteID(rand.Uint32()) }

	out := make([]routing.Rule, n)
	for i := range out {
		out[i] = routing.IntermediaryForwardRule(keepAlive, randRtID(), randRtID(), uuid.New())
	}
	return out
}
