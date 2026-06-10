// Package router pkg/router/setupnode_teardown_test.go
package router

import (
	"context"
	"sync"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// delRulesRecorder is a mock router RPCGateway that records the KeyRouteIDs it
// is asked to install (AddIntermediaryRules) and the RouteIDs it is asked to
// delete (DelRules), so a test can assert that a setup teardown removes exactly
// what it installed — i.e. leaves no orphan rules and over-deletes nothing.
type delRulesRecorder struct {
	mu      sync.Mutex
	added   []routing.RouteID
	deleted []routing.RouteID
}

func (g *delRulesRecorder) AddIntermediaryRules(rules []routing.Rule, ok *bool) error {
	g.mu.Lock()
	for _, r := range rules {
		g.added = append(g.added, r.KeyRouteID())
	}
	g.mu.Unlock()
	*ok = true
	return nil
}

func (g *delRulesRecorder) DelRules(routeIDs []routing.RouteID, ok *bool) error {
	g.mu.Lock()
	g.deleted = append(g.deleted, routeIDs...)
	g.mu.Unlock()
	*ok = true
	return nil
}

func newRecorderReserver(t *testing.T, n int) (IDReserver, RulesMap, map[cipher.PubKey]*delRulesRecorder) {
	pks := randPKs(n)
	gateways := make(map[cipher.PubKey]interface{}, len(pks))
	recorders := make(map[cipher.PubKey]*delRulesRecorder, len(pks))
	for _, pk := range pks {
		g := &delRulesRecorder{}
		gateways[pk] = g
		recorders[pk] = g
	}
	return newMockReserver(t, gateways), randRulesMap(pks), recorders
}

// TestInstalledRulesTeardownDeletesEverythingInstalled verifies the
// orphan-rule fix: rules recorded by BroadcastIntermediaryRules are torn down
// exactly by installedRules.teardown — every installed rule is deleted, and no
// rule that was not installed is deleted.
func TestInstalledRulesTeardownDeletesEverythingInstalled(t *testing.T) {
	rtIDR, rules, recorders := newRecorderReserver(t, 3)
	installed := newInstalledRules()
	ctx := context.Background()

	require.NoError(t, BroadcastIntermediaryRules(ctx, logrus.New(), rtIDR, rules, installed))
	installed.teardown(ctx, logrus.New(), rtIDR)

	for pk, g := range recorders {
		g.mu.Lock()
		assert.NotEmpty(t, g.added, "hop %s should have installed rules", pk)
		assert.ElementsMatch(t, g.added, g.deleted,
			"hop %s: every installed rule must be torn down with no orphans and no over-deletion", pk)
		g.mu.Unlock()
	}
}

// TestInstalledRulesNoTeardownOnSuccess is the negative control: the success
// path records the installed set but never calls teardown, so nothing is
// deleted.
func TestInstalledRulesNoTeardownOnSuccess(t *testing.T) {
	rtIDR, rules, recorders := newRecorderReserver(t, 2)
	installed := newInstalledRules()

	require.NoError(t, BroadcastIntermediaryRules(context.Background(), logrus.New(), rtIDR, rules, installed))
	// success path: teardown is intentionally NOT called.

	for pk, g := range recorders {
		g.mu.Lock()
		assert.Empty(t, g.deleted, "hop %s: nothing should be deleted on the success path", pk)
		g.mu.Unlock()
	}
}
