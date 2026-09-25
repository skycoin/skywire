// Package routing pkg/routing/rule_validate_test.go
package routing

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

func validRules() []Rule {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	return []Rule{
		ConsumeRule(time.Minute, 1, pk1, pk2, 3, 4),
		ForwardRule(time.Minute, 2, 5, uuid.New(), pk1, pk2, 3, 4),
		IntermediaryForwardRule(time.Minute, 6, 7, uuid.New()),
	}
}

// malformedRules are rules a remote peer could send: each is too short for its
// type or of a type no accessor knows.
func malformedRules() map[string]Rule {
	out := map[string]Rule{
		"empty":            {},
		"shorter than hdr": make(Rule, RuleHeaderSize-1),
	}
	for _, r := range validRules() {
		// One byte short of the minimum for the type: the NextTransportID
		// read on a forward/intermediary rule is the last field.
		want, _ := minRuleSize(r.Type())
		out["truncated "+r.Type().String()] = append(Rule(nil), r[:want-1]...)
	}
	unknown := append(Rule(nil), validRules()[1]...)
	unknown[8] = 7
	out["unknown type"] = unknown
	return out
}

func TestRuleValidate(t *testing.T) {
	for _, r := range validRules() {
		require.NoError(t, r.Validate(), r.Type().String())
	}
	for name, r := range malformedRules() {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, r.Validate(), ErrInvalidRule)
		})
	}
}

// Every accessor is safe on a rule that passed Validate.
func TestRuleValidate_AccessorsDoNotPanic(t *testing.T) {
	for _, r := range validRules() {
		want, _ := minRuleSize(r.Type())
		r = r[:want] // the shortest encoding Validate accepts
		require.NoError(t, r.Validate())
		require.NotPanics(t, func() {
			_ = r.String()
			_ = r.Summary()
		}, r.Type().String())
	}
}

func TestEdgeRulesValidate(t *testing.T) {
	v := validRules()
	require.NoError(t, EdgeRules{Forward: v[1], Reverse: v[0]}.Validate())
	require.ErrorIs(t, EdgeRules{Forward: v[0], Reverse: v[0]}.Validate(), ErrInvalidRule, "consume rule as forward")
	require.ErrorIs(t, EdgeRules{Forward: v[1], Reverse: v[2]}.Validate(), ErrInvalidRule, "intermediary as reverse")
	for name, bad := range malformedRules() {
		require.ErrorIs(t, EdgeRules{Forward: bad, Reverse: v[0]}.Validate(), ErrInvalidRule, name)
		require.ErrorIs(t, EdgeRules{Forward: v[1], Reverse: bad}.Validate(), ErrInvalidRule, name)
	}
}

func TestDeserializeRules_RejectsMalformed(t *testing.T) {
	for name, bad := range malformedRules() {
		t.Run(name, func(t *testing.T) {
			data := SerializeRules(append(validRules(), bad))
			var err error
			require.NotPanics(t, func() { _, err = DeserializeRules(data) })
			require.ErrorIs(t, err, ErrInvalidRule)
		})
	}
	got, err := DeserializeRules(SerializeRules(validRules()))
	require.NoError(t, err)
	require.Equal(t, validRules()[2].Type(), got[2].Type())
}

func TestTableSaveRule_RejectsMalformed(t *testing.T) {
	rt := NewTable(logging.MustGetLogger("test_rt"))
	for name, bad := range malformedRules() {
		var err error
		require.NotPanics(t, func() { err = rt.SaveRule(bad) }, name)
		require.ErrorIs(t, err, ErrInvalidRule, name)
	}
	require.Zero(t, rt.Count())
}
