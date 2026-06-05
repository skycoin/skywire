// Package routing pkg/routing/cascade_test.go
package routing

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestCascadeSetup_MarshalRoundTrip_WithEdgeDesc verifies EdgeDesc survives a
// marshal/unmarshal round-trip and that IsEdge reflects it.
func TestCascadeSetup_MarshalRoundTrip_WithEdgeDesc(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	var desc RouteDescriptor
	for i := range desc {
		desc[i] = byte(i + 1)
	}
	cs := &CascadeSetup{
		Phase:     CascadePhaseInstall,
		SessionID: 7,
		RSNPK:     pk,
		Nonce:     3,
		ReserveN:  0,
		RelayTpID: uuid.Nil, // terminal
		RuleData:  []byte{1, 2, 3, 4},
		EdgeDesc:  desc,
		Payload:   nil,
	}
	b, err := cs.Marshal()
	require.NoError(t, err)

	got, err := UnmarshalCascadeSetup(b)
	require.NoError(t, err)
	assert.Equal(t, desc, got.EdgeDesc)
	assert.True(t, got.IsEdge())
	assert.Equal(t, cs.RuleData, got.RuleData)
	assert.True(t, got.IsTerminal())
}

// TestCascadeSetup_BackwardCompat_NoTrailingEdgeDesc simulates a message from a
// node that predates EdgeDesc: the trailing descriptor bytes are absent. The
// new parser must accept it and leave EdgeDesc zero (IsEdge()==false), proving
// old intermediaries interoperate with new source/destination.
func TestCascadeSetup_BackwardCompat_NoTrailingEdgeDesc(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	cs := &CascadeSetup{
		Phase:     CascadePhaseInstall,
		SessionID: 9,
		RSNPK:     pk,
		Nonce:     1,
		RelayTpID: uuid.Nil,
		RuleData:  []byte{9, 9},
	}
	b, err := cs.Marshal()
	require.NoError(t, err)

	// Drop the trailing EdgeDesc to mimic the old wire format.
	require.Greater(t, len(b), routeDescriptorSize)
	oldWire := b[:len(b)-routeDescriptorSize]

	got, err := UnmarshalCascadeSetup(oldWire)
	require.NoError(t, err)
	assert.False(t, got.IsEdge(), "missing trailing EdgeDesc must parse as non-edge")
	assert.Equal(t, cs.RuleData, got.RuleData)
}
