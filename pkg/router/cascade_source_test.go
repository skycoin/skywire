// Package router pkg/router/cascade_source_test.go
package router

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// buildTestBiRoute builds a 2-hop bidirectional route A -> B -> C (forward)
// and C -> B -> A (reverse), returning the route plus the per-hop PKs.
func buildTestBiRoute(t *testing.T) (routing.BidirectionalRoute, cipher.PubKey, cipher.PubKey, cipher.PubKey) {
	t.Helper()
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	pkC, _ := cipher.GenerateKeyPair()

	desc := routing.NewRouteDescriptor(pkA, pkC, 100, 200)
	fwd := makeHops(pkA, pkB, pkC)
	rev := makeHops(pkC, pkB, pkA)

	biRt := routing.BidirectionalRoute{
		Desc:    desc,
		Forward: fwd,
		Reverse: rev,
	}
	require.NoError(t, biRt.Check())
	return biRt, pkA, pkB, pkC
}

func rsnBuilder(t *testing.T) (*CascadeBuilder, cipher.PubKey) {
	t.Helper()
	rsnPK, rsnSK := cipher.GenerateKeyPair()
	cb := NewCascadeBuilder(logging.MustGetLogger("test_rsn"), rsnPK, rsnSK, nil)
	return cb, rsnPK
}

// verifyReserveChain walks a nested reserve cascade and asserts each layer is
// signed by the RSN for its target visor PK. targets is the ordered list of
// visor PKs the cascade addresses: [hops[0].From, hops[0].To, hops[1].To, ...].
func verifyReserveChain(t *testing.T, payload []byte, rsnPK cipher.PubKey, targets []cipher.PubKey) {
	t.Helper()
	for i, target := range targets {
		cs, err := routing.UnmarshalCascadeSetup(payload)
		require.NoErrorf(t, err, "unmarshal layer %d", i)
		assert.Equalf(t, routing.CascadePhaseReserve, cs.Phase, "layer %d phase", i)
		assert.Equalf(t, rsnPK, cs.RSNPK, "layer %d RSNPK", i)
		require.NoErrorf(t, cs.Verify(target), "layer %d signature for target %s", i, target)
		// terminal layer carries no further payload
		if i == len(targets)-1 {
			assert.True(t, cs.IsTerminal(), "last layer must be terminal")
		} else {
			require.NotEmptyf(t, cs.Payload, "layer %d must wrap an inner payload", i)
		}
		payload = cs.Payload
	}
}

func TestSignReserveCascades_SignedBytes(t *testing.T) {
	cb, rsnPK := rsnBuilder(t)
	biRt, pkA, pkB, pkC := buildTestBiRoute(t)

	fwdSession, fwdBytes, revSession, revBytes, err := signReserveCascades(cb, biRt)
	require.NoError(t, err)
	require.NotZero(t, fwdSession)
	require.NotZero(t, revSession)
	require.NotEqual(t, fwdSession, revSession, "forward and reverse must use distinct sessions")
	require.NotEmpty(t, fwdBytes)
	require.NotEmpty(t, revBytes)

	// Forward targets: A (source) -> B -> C
	verifyReserveChain(t, fwdBytes, rsnPK, []cipher.PubKey{pkA, pkB, pkC})
	// Reverse targets: re-oriented source-first so the SOURCE (A) injects it
	// over its own transports — A -> B -> C, NOT the logical reverse C -> B -> A.
	verifyReserveChain(t, revBytes, rsnPK, []cipher.PubKey{pkA, pkB, pkC})
}

func TestSignReserveCascades_WrongTargetFailsVerify(t *testing.T) {
	cb, _ := rsnBuilder(t)
	biRt, _, _, _ := buildTestBiRoute(t)

	_, fwdBytes, _, _, err := signReserveCascades(cb, biRt)
	require.NoError(t, err)

	cs, err := routing.UnmarshalCascadeSetup(fwdBytes)
	require.NoError(t, err)

	// A different visor PK must not validate the first layer's signature.
	otherPK, _ := cipher.GenerateKeyPair()
	assert.Error(t, cs.Verify(otherPK))
}

func TestSignInstallCascades_RulesAndEdge(t *testing.T) {
	cb, _ := rsnBuilder(t)
	biRt, pkA, pkB, pkC := buildTestBiRoute(t)

	fwdSession, _, revSession, _, err := signReserveCascades(cb, biRt)
	require.NoError(t, err)

	// Simulate route IDs collected from each hop. computeReserveCount gives
	// each of A, B, C two IDs across both paths; provide enough per path.
	// Forward path PKs in order: A, B, C ; reverse path PKs: C, B, A.
	fwdIDs := []routing.RouteID{10, 11, 12}
	revIDs := []routing.RouteID{20, 21, 22}

	fwdBytes, revBytes, initEdge, err := signInstallCascades(cb, biRt, fwdSession, revSession, fwdIDs, revIDs)
	require.NoError(t, err)
	require.NotEmpty(t, fwdBytes)
	require.NotEmpty(t, revBytes)

	// InitEdge must be populated with non-empty forward/reverse rules.
	require.NotEmpty(t, initEdge.Forward)
	require.NotEmpty(t, initEdge.Reverse)

	// Each install layer must be install-phase, RSN-signed for its target,
	// and (except the terminal) carry rule data + an inner payload.
	fwdTargets := []cipher.PubKey{pkA, pkB, pkC}
	payload := fwdBytes
	for i, target := range fwdTargets {
		cs, err := routing.UnmarshalCascadeSetup(payload)
		require.NoErrorf(t, err, "install layer %d", i)
		assert.Equal(t, routing.CascadePhaseInstall, cs.Phase)
		require.NoError(t, cs.Verify(target))
		payload = cs.Payload
	}
}

func TestSignInstallCascades_NotEnoughIDs(t *testing.T) {
	cb, _ := rsnBuilder(t)
	biRt, _, _, _ := buildTestBiRoute(t)
	fwdSession, _, revSession, _, err := signReserveCascades(cb, biRt)
	require.NoError(t, err)

	// Too few IDs -> reconstruct must fail.
	_, _, _, err = signInstallCascades(cb, biRt, fwdSession, revSession,
		[]routing.RouteID{1}, []routing.RouteID{2})
	require.Error(t, err)
}

func TestIsUnimplementedRPC(t *testing.T) {
	assert.False(t, isUnimplementedRPC(nil))
	assert.False(t, isUnimplementedRPC(errors.New("some other error")))
	assert.True(t, isUnimplementedRPC(errors.New("rpc: can't find method SetupRPCGateway.CascadeSignReserve")))
	assert.True(t, isUnimplementedRPC(errors.New("rpc: can't find service SetupRPCGateway")))
}

func TestRunSourceCascade_NilBuilder(t *testing.T) {
	biRt, _, _, _ := buildTestBiRoute(t)
	_, err := runSourceCascade(t.Context(), logging.MustGetLogger("test"), nil, nil, biRt)
	require.Error(t, err)
}

// TestAckRegistry_Dispatch verifies the shared ack registry correlates a
// dispatched ACK packet back to a registered waiter and drops unknown ones.
func TestAckRegistry_Dispatch(t *testing.T) {
	reg := newAckRegistry()
	ch := reg.register(7)

	ack := &routing.CascadeAck{SessionID: 7, RouteIDs: []routing.RouteID{42}}
	data, err := ack.Marshal()
	require.NoError(t, err)
	pkt, err := routing.MakeCascadeAckPacket(data)
	require.NoError(t, err)

	assert.True(t, reg.dispatch(7, pkt), "known session should dispatch")
	select {
	case got := <-ch:
		decoded, err := routing.UnmarshalCascadeAck(got.Payload())
		require.NoError(t, err)
		assert.Equal(t, []routing.RouteID{42}, decoded.RouteIDs)
	default:
		t.Fatal("expected ACK on wait channel")
	}

	assert.False(t, reg.dispatch(999, pkt), "unknown session should not dispatch")
}

// TestSourceBuilderSharesHandlerRegistry verifies a source-side builder and a
// cascade handler can share one ack registry (the source visor has a single
// registered handler that must deliver ACKs to the builder's sends).
func TestSourceBuilderSharesHandlerRegistry(t *testing.T) {
	localPK, _ := cipher.GenerateKeyPair()
	ch := NewCascadeHandler(logging.MustGetLogger("test_handler"), localPK, nil, nil, nil, nil)
	srcCB := NewSourceCascadeBuilder(logging.MustGetLogger("test_src"), nil, ch.AckRegistry())

	// Builder and handler must observe the same registry instance.
	assert.Same(t, ch.AckRegistry(), srcCB.AckRegistry())

	// A send-side registration must be visible to a handler-side dispatch.
	waitCh := srcCB.acks.register(5)
	ack := &routing.CascadeAck{SessionID: 5}
	data, err := ack.Marshal()
	require.NoError(t, err)
	pkt, err := routing.MakeCascadeAckPacket(data)
	require.NoError(t, err)
	assert.True(t, ch.acks.dispatch(5, pkt))
	select {
	case <-waitCh:
	default:
		t.Fatal("handler dispatch did not reach builder's waiter")
	}
}

// TestReserveCascade_OutermostLayerAddressesSource is the regression test for
// the source-driven cascade signature bug: the RSN builds the nested reserve
// cascade with the SOURCE (hops[0].From) as the OUTERMOST layer. Shipping that
// layer verbatim to the first hop made the hop reject it with "RSN signature
// verification failed" — because it is signed for the source's PK, not the
// hop's. The source must consume its own layer (ProcessLocalOrigin).
func TestReserveCascade_OutermostLayerAddressesSource(t *testing.T) {
	cb, _ := rsnBuilder(t)
	biRt, pkA, pkB, _ := buildTestBiRoute(t)

	_, fwdBytes, _, _, err := signReserveCascades(cb, biRt)
	require.NoError(t, err)

	outer, err := routing.UnmarshalCascadeSetup(fwdBytes)
	require.NoError(t, err)
	require.NoError(t, outer.Verify(pkA), "outermost layer must verify for the source (pkA)")
	require.Error(t, outer.Verify(pkB),
		"outermost layer must NOT verify for the first hop (pkB) — the bug that 0/N'd source-driven setup")
}

// TestProcessLocalOrigin_TerminalReserve verifies the source consumes a layer
// addressed to itself: it passes the trusted-RSN + own-PK signature checks and
// reserves its own route IDs. (Terminal layer => no relay, so no tm needed.)
func TestProcessLocalOrigin_TerminalReserve(t *testing.T) {
	rsnPK, rsnSK := cipher.GenerateKeyPair()
	srcPK, _ := cipher.GenerateKeyPair()

	msg := &routing.CascadeSetup{
		Phase:     routing.CascadePhaseReserve,
		SessionID: 42,
		RSNPK:     rsnPK,
		Nonce:     1,
		ReserveN:  2,
		// RelayTpID zero => terminal, no downstream relay
	}
	require.NoError(t, msg.Sign(srcPK, rsnSK))
	payload, err := msg.Marshal()
	require.NoError(t, err)

	ch := NewCascadeHandler(logging.MustGetLogger("test_src"), srcPK,
		[]cipher.PubKey{rsnPK}, routing.NewTable(logging.MustGetLogger("test_rt")), nil, nil)

	ack, err := ch.ProcessLocalOrigin(payload)
	require.NoError(t, err)
	assert.Equal(t, uint64(42), ack.SessionID)
	assert.Len(t, ack.RouteIDs, 2, "source must reserve its own route IDs")
}

// TestProcessLocalOrigin_Rejections covers the two guard paths that fail before
// any relay: an untrusted RSN and a signature not addressed to this visor.
func TestProcessLocalOrigin_Rejections(t *testing.T) {
	rsnPK, rsnSK := cipher.GenerateKeyPair()
	srcPK, _ := cipher.GenerateKeyPair()
	otherPK, _ := cipher.GenerateKeyPair()
	rt := routing.NewTable(logging.MustGetLogger("test_rt"))
	ch := NewCascadeHandler(logging.MustGetLogger("test_src"), srcPK,
		[]cipher.PubKey{rsnPK}, rt, nil, nil)

	mk := func(target cipher.PubKey, rsn cipher.PubKey) []byte {
		msg := &routing.CascadeSetup{
			Phase: routing.CascadePhaseReserve, SessionID: 1, RSNPK: rsn, Nonce: 1, ReserveN: 1,
		}
		require.NoError(t, msg.Sign(target, rsnSK))
		b, err := msg.Marshal()
		require.NoError(t, err)
		return b
	}

	// RSNPK not in the trusted set.
	_, err := ch.ProcessLocalOrigin(mk(srcPK, otherPK))
	require.ErrorIs(t, err, routing.ErrCascadeUntrustedRSN)

	// Trusted RSN, but the layer is signed for otherPK, not us (srcPK).
	_, err = ch.ProcessLocalOrigin(mk(otherPK, rsnPK))
	require.ErrorIs(t, err, routing.ErrCascadeSigInvalid)
}

// simulateReserveCascade walks a nested reserve cascade the way the real hops
// would: each layer is verified against its target PK in source-first order,
// the target "reserves" ReserveN fresh route IDs, and the ACK accumulates those
// IDs source-first (each hop prepends its own, which equals appending in target
// order). Returns the collected IDs.
func simulateReserveCascade(t *testing.T, payload []byte, targets []cipher.PubKey, nextID *routing.RouteID) []routing.RouteID {
	t.Helper()
	var acc []routing.RouteID
	for i, target := range targets {
		cs, err := routing.UnmarshalCascadeSetup(payload)
		require.NoErrorf(t, err, "reserve layer %d unmarshal", i)
		require.Equalf(t, routing.CascadePhaseReserve, cs.Phase, "reserve layer %d phase", i)
		require.NoErrorf(t, cs.Verify(target), "reserve layer %d must be signed for target %s", i, target)
		for j := uint8(0); j < cs.ReserveN; j++ {
			acc = append(acc, *nextID)
			*nextID++
		}
		if i == len(targets)-1 {
			require.Truef(t, cs.IsTerminal(), "reserve last layer must be terminal")
		} else {
			require.NotEmptyf(t, cs.Payload, "reserve layer %d must wrap inner payload", i)
			payload = cs.Payload
		}
	}
	return acc
}

// assertInstallSourceFirst walks an install cascade and asserts each layer is
// signed for its source-first target PK.
func assertInstallSourceFirst(t *testing.T, payload []byte, targets []cipher.PubKey) {
	t.Helper()
	for i, target := range targets {
		cs, err := routing.UnmarshalCascadeSetup(payload)
		require.NoErrorf(t, err, "install layer %d unmarshal", i)
		require.Equalf(t, routing.CascadePhaseInstall, cs.Phase, "install layer %d phase", i)
		require.NoErrorf(t, cs.Verify(target), "install layer %d must be signed for target %s", i, target)
		if i < len(targets)-1 {
			payload = cs.Payload
		}
	}
}

// TestSourceCascade_BothDirectionsSourceFirst_EndToEnd is the deterministic
// regression test for the source-driven cascade bugs found 2026-06-04:
//   - the reverse cascade was built dst-first (signed for the destination), so
//     the source could not consume its outermost layer ("rev reserve: RSN
//     signature verification failed");
//   - each cascade reserved the COMBINED per-PK count while newCascadeIDReserver
//     consumed one ID per PK, misaligning route IDs when count>1.
//
// It drives the real RSN-side build/sign/ID-reserve/rule-generation path and
// asserts BOTH cascades are source-first and the install signing — which runs
// newCascadeIDReserver + GenerateRules — succeeds (i.e. the IDs line up).
func TestSourceCascade_BothDirectionsSourceFirst_EndToEnd(t *testing.T) {
	cb, _ := rsnBuilder(t)
	biRt, pkA, pkB, pkC := buildTestBiRoute(t) // forward A -> B -> C

	fwdSession, fwdBytes, revSession, revBytes, err := signReserveCascades(cb, biRt)
	require.NoError(t, err)

	// Both the forward AND the re-oriented reverse cascade must address the
	// SOURCE (pkA) outermost, then pkB, then pkC.
	srcFirst := []cipher.PubKey{pkA, pkB, pkC}

	var nextID routing.RouteID = 1
	fwdIDs := simulateReserveCascade(t, fwdBytes, srcFirst, &nextID)
	revIDs := simulateReserveCascade(t, revBytes, srcFirst, &nextID)

	// Each node reserves once per cascade (singlePathReserveCount); two cascades
	// => two route IDs per node, matching what GenerateRules pops per PK.
	require.Len(t, fwdIDs, 3, "one fwd ID per node")
	require.Len(t, revIDs, 3, "one rev ID per node")

	// Signing the install runs newCascadeIDReserver + GenerateRules. If the IDs
	// misaligned (the count bug) this returns ErrNoKey.
	fwdInst, revInst, initEdge, err := signInstallCascades(cb, biRt, fwdSession, revSession, fwdIDs, revIDs)
	require.NoError(t, err, "install signing must succeed (route IDs aligned)")
	require.NotEmpty(t, fwdInst)
	require.NotEmpty(t, revInst)
	require.NotEmpty(t, initEdge.Forward, "initiating edge must carry a forward rule")
	require.NotEmpty(t, initEdge.Reverse, "initiating edge must carry a reverse rule")

	// Install cascades are also source-first.
	assertInstallSourceFirst(t, fwdInst, srcFirst)
	assertInstallSourceFirst(t, revInst, srcFirst)
}

// TestInstallCascade_EdgeMarkedOnDestinationOnly verifies the route-group fix:
// the forward install cascade stamps EdgeDesc (= the forward route descriptor)
// onto the TERMINAL hop only — the responding destination — so that hop calls
// IntroduceRules (creates a route group); intermediaries and the reverse
// cascade carry no edge marking. Without this, the destination only installs
// forwarding rules, never creates a route group, and the source's noise
// handshake over the route times out.
func TestInstallCascade_EdgeMarkedOnDestinationOnly(t *testing.T) {
	cb, _ := rsnBuilder(t)
	biRt, pkA, pkB, pkC := buildTestBiRoute(t) // A -> B -> C
	fwdRt, _ := biRt.ForwardAndReverse()

	fwdSession, _, revSession, _, err := signReserveCascades(cb, biRt)
	require.NoError(t, err)
	fwdInst, revInst, _, err := signInstallCascades(cb, biRt, fwdSession, revSession,
		[]routing.RouteID{10, 11, 12}, []routing.RouteID{20, 21, 22})
	require.NoError(t, err)

	srcFirst := []cipher.PubKey{pkA, pkB, pkC}

	// Forward install: only the terminal (pkC = destination) is an edge.
	payload := fwdInst
	for i, target := range srcFirst {
		cs, err := routing.UnmarshalCascadeSetup(payload)
		require.NoErrorf(t, err, "fwd install layer %d", i)
		require.NoError(t, cs.Verify(target))
		if i == len(srcFirst)-1 {
			require.True(t, cs.IsEdge(), "destination layer must be an edge")
			require.Equal(t, fwdRt.Desc, cs.EdgeDesc, "edge desc must be the forward route descriptor")
			rules, err := routing.DeserializeRules(cs.RuleData)
			require.NoError(t, err)
			require.Len(t, rules, 2, "edge carries exactly [Forward, Reverse]")
		} else {
			require.Falsef(t, cs.IsEdge(), "intermediary layer %d must not be an edge", i)
			payload = cs.Payload
		}
	}

	// Reverse install: no layer is an edge (route group already introduced).
	payload = revInst
	for i, target := range srcFirst {
		cs, err := routing.UnmarshalCascadeSetup(payload)
		require.NoErrorf(t, err, "rev install layer %d", i)
		require.NoError(t, cs.Verify(target))
		require.Falsef(t, cs.IsEdge(), "reverse install layer %d must not be an edge", i)
		if i < len(srcFirst)-1 {
			payload = cs.Payload
		}
	}
}
