// Package routing pkg/routing/rule_test.go
package routing

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

func TestConsumeRule(t *testing.T) {
	keepAlive := 2 * time.Minute
	localPK, _ := cipher.GenerateKeyPair()
	remotePK, _ := cipher.GenerateKeyPair()

	rule := ConsumeRule(keepAlive, 1, localPK, remotePK, 2, 3)

	assert.Equal(t, keepAlive, rule.KeepAlive())
	assert.Equal(t, RuleReverse, rule.Type())
	assert.Equal(t, RouteID(1), rule.KeyRouteID())

	rd := rule.RouteDescriptor()
	assert.Equal(t, localPK, rd.SrcPK())
	assert.Equal(t, remotePK, rd.DstPK())
	assert.Equal(t, Port(3), rd.DstPort())
	assert.Equal(t, Port(2), rd.SrcPort())

	rule.SetKeyRouteID(4)
	assert.Equal(t, RouteID(4), rule.KeyRouteID())
}

func TestForwardRule(t *testing.T) {
	trID := uuid.New()
	keepAlive := 2 * time.Minute
	pk, _ := cipher.GenerateKeyPair()

	rule := ForwardRule(keepAlive, 1, 2, trID, cipher.PubKey{}, pk, 3, 4)

	assert.Equal(t, keepAlive, rule.KeepAlive())
	assert.Equal(t, RuleForward, rule.Type())
	assert.Equal(t, RouteID(1), rule.KeyRouteID())
	assert.Equal(t, RouteID(2), rule.NextRouteID())
	assert.Equal(t, trID, rule.NextTransportID())

	rd := rule.RouteDescriptor()
	assert.Equal(t, pk, rd.DstPK())
	assert.Equal(t, Port(4), rd.DstPort())
	assert.Equal(t, Port(3), rd.SrcPort())

	rule.SetKeyRouteID(5)
	assert.Equal(t, RouteID(5), rule.KeyRouteID())
}

func TestIntermediaryForwardRule(t *testing.T) {
	trID := uuid.New()
	keepAlive := 2 * time.Minute

	rule := IntermediaryForwardRule(keepAlive, 1, 2, trID)

	assert.Equal(t, keepAlive, rule.KeepAlive())
	assert.Equal(t, RuleIntermediary, rule.Type())
	assert.Equal(t, RouteID(1), rule.KeyRouteID())
	assert.Equal(t, RouteID(2), rule.NextRouteID())
	assert.Equal(t, trID, rule.NextTransportID())

	rule.SetKeyRouteID(3)
	assert.Equal(t, RouteID(3), rule.KeyRouteID())
}
