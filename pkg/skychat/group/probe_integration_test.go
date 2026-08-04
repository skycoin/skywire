// Package group pkg/skychat/group/probe_integration_test.go
//
// The dmsg-backed lane for the describe port: what a short
// skychat://<pk>/<group-id> address can be turned into, and what it must
// never be turned into.
//
// These need a real transport for the same reason the admission tests do
// — the asker's identity comes from the authenticated stream, and the
// ban answer is about that identity — plus one case that exists purely
// to pin a security property: a real join request arriving on this port
// must be refused, not honored, or every group would have a second door
// that skips the proof-of-work and rate gates.
//
// Reuses the harness in manager_integration_test.go; skipped under
// -short like every other dmsg-backed test in this package.
package group

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/skychat/message"
)

// probeCtxFor is the per-call budget for a describe in tests. Generous
// relative to probeTimeout so a slow CI box fails on the assertion
// rather than on the clock.
func probeCtxFor(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// The headline case: a group's kind, admission policy and feed port are
// all learnable from its ID alone. Without this a short address is inert
// — the port is random per group, so nothing can be dialed from it.
func TestProbe_DescribesGroupFromIDAlone(t *testing.T) {
	tests := []struct {
		name       string
		kind       Kind
		wantPolicy JoinPolicy
		wantMode   Mode
	}{
		{"public group", KindPublic, JoinOpen, ModePublic},
		{"private group", KindPrivate, JoinApproval, ModePrivate},
		{"channel", KindChannel, JoinOpen, ModePublic},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, b := newGroupEnv(t)
			a.mgr.StartProbeListener()

			rec, err := a.mgr.Create(tt.name, tt.kind, nil)
			require.NoError(t, err, "Create")

			ctx, cancel := probeCtxFor(t)
			defer cancel()
			d, err := b.mgr.ProbeGroup(ctx, a.pk, rec.ID)
			require.NoError(t, err, "ProbeGroup")

			require.Equal(t, rec.ID, d.ID)
			require.Equal(t, a.pk, d.HostPK)
			require.Equal(t, tt.name, d.Name, "the describe must carry the real name")
			require.Equal(t, tt.kind, d.Kind)
			require.Equal(t, tt.wantMode, d.Mode)
			require.Equal(t, tt.wantPolicy, d.Policy)
			require.Equal(t, rec.Port, d.Port, "the feed port is the whole reason to probe")
			require.False(t, d.Banned)

			// A describe grants nothing: the key stays behind the
			// admission decision, and the roster is not named at all.
			inv := d.Invite()
			require.Empty(t, inv.AESKey, "a describe must never hand over the group key")
			require.Empty(t, inv.Admins, "a describe must not repeat an unverified admin list")
			require.Equal(t, tt.kind, inv.Kind)
		})
	}
}

// A public group is joinable from a short address with no invite link at
// all — the point of the whole feature.
func TestProbe_JoinByAddressAdmitsToPublicGroup(t *testing.T) {
	a, b := newGroupEnv(t)
	a.mgr.StartProbeListener()

	rec, err := a.mgr.Create("open room", KindPublic, nil)
	require.NoError(t, err, "Create")
	require.NotContains(t, rec.Members, b.pk, "B must not be pre-admitted for this test to mean anything")

	ctx, cancel := probeCtxFor(t)
	defer cancel()
	joined, err := b.mgr.JoinByAddress(ctx, a.pk, rec.ID)
	require.NoError(t, err, "JoinByAddress with no invite link")
	require.Equal(t, StatusActive, joined.Status)
	require.Equal(t, RoleMember, joined.Role)
	require.Equal(t, KindPublic, joined.Kind)

	owner, ok, err := a.mgr.Get(rec.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Contains(t, owner.Members, b.pk, "join-by-address did not reach the roster")

	// Idempotent, like the link-based join.
	again, err := b.mgr.JoinByAddress(ctx, a.pk, rec.ID)
	require.NoError(t, err, "second JoinByAddress")
	require.Equal(t, joined.ID, again.ID)
}

// A private group must queue rather than admit, exactly as it does for a
// link. The address form must not become a way around approval.
func TestProbe_JoinByAddressQueuesOnPrivateGroup(t *testing.T) {
	a, b := newGroupEnv(t)
	a.mgr.StartProbeListener()

	rec, err := a.mgr.Create("private room", KindPrivate, nil)
	require.NoError(t, err, "Create")

	ctx, cancel := probeCtxFor(t)
	defer cancel()
	joined, err := b.mgr.JoinByAddress(ctx, a.pk, rec.ID)
	require.NoError(t, err, "JoinByAddress should queue, not fail")
	require.Equal(t, StatusAwaitingApproval, joined.Status,
		"an address must not admit to a group that requires approval")
	require.Empty(t, joined.AESKey, "no key before approval")

	reqs, err := a.mgr.PendingJoins(rec.ID)
	require.NoError(t, err, "PendingJoins")
	require.Len(t, reqs, 1, "the request should be on the admin's queue")
	require.Equal(t, b.pk, reqs[0].PK)
}

// Joining a channel by address must produce a record that knows it is a
// channel. Mode cannot express this — a channel and a public group are
// both ModePublic — so a member whose Kind degraded would come up with
// an unlocked composer and publish leaves every other member drops.
func TestProbe_JoinByAddressKeepsChannelAdminsOnly(t *testing.T) {
	a, b := newGroupEnv(t)
	a.mgr.StartProbeListener()

	rec, err := a.mgr.Create("announcements", KindChannel, nil)
	require.NoError(t, err, "Create")

	ctx, cancel := probeCtxFor(t)
	defer cancel()
	joined, err := b.mgr.JoinByAddress(ctx, a.pk, rec.ID)
	require.NoError(t, err, "JoinByAddress")
	require.Equal(t, KindChannel, joined.Kind, "the joined record must remember it is a channel")
	require.True(t, joined.IsChannel())

	canPost, reason := joined.CanPost(b.pk)
	require.False(t, canPost, "a plain member must not be able to post to a channel")
	require.Contains(t, reason, "channel")

	owner, ok, err := a.mgr.Get(rec.ID)
	require.NoError(t, err)
	require.True(t, ok)
	ownerCanPost, _ := owner.CanPost(a.pk)
	require.True(t, ownerCanPost, "the channel's admin must still be able to post")
}

// A banned asker gets a description carrying that fact rather than a
// bare failure, so the UI can name the group and explain instead of
// offering a join that is certain to be refused.
func TestProbe_BannedAskerIsToldSo(t *testing.T) {
	a, b := newGroupEnv(t)
	a.mgr.StartProbeListener()

	rec, err := a.mgr.Create("open room", KindPublic, nil)
	require.NoError(t, err, "Create")
	_, err = a.mgr.BanMember(rec.ID, b.pk)
	require.NoError(t, err, "BanMember")

	ctx, cancel := probeCtxFor(t)
	defer cancel()
	d, err := b.mgr.ProbeGroup(ctx, a.pk, rec.ID)
	require.NoError(t, err, "a ban is a description, not a probe failure")
	require.True(t, d.Banned)
	require.Equal(t, rec.ID, d.ID)

	// And the join it would have offered is refused.
	_, err = b.mgr.JoinByAddress(ctx, a.pk, rec.ID)
	require.ErrorIs(t, err, ErrJoinBanned)
}

// An ID the host does not hold is a wrong address, not an unreachable
// one — a caller has to be able to say "no such group" rather than
// "try again later".
func TestProbe_UnknownGroupIsNotFound(t *testing.T) {
	a, b := newGroupEnv(t)
	a.mgr.StartProbeListener()

	ctx, cancel := probeCtxFor(t)
	defer cancel()
	_, err := b.mgr.ProbeGroup(ctx, a.pk, uuid.NewString())
	require.ErrorIs(t, err, ErrProbeNoSuchGroup)
}

// A group the host has LEFT must answer the same way as one it never
// had: a terminal record is not something anyone can join.
func TestProbe_LeftGroupIsNotFound(t *testing.T) {
	a, b := newGroupEnv(t)
	a.mgr.StartProbeListener()

	rec, err := a.mgr.Create("closing down", KindPublic, nil)
	require.NoError(t, err, "Create")
	require.NoError(t, a.mgr.Delete(rec.ID), "Delete")

	ctx, cancel := probeCtxFor(t)
	defer cancel()
	_, err = b.mgr.ProbeGroup(ctx, a.pk, rec.ID)
	require.ErrorIs(t, err, ErrProbeNoSuchGroup)
}

// The security property this port exists under: it describes, and never
// decides. A genuine join request delivered here must be refused, or the
// describe port would be a second admission door — one that skips the
// proof-of-work and rate-limit gates guarding the real one.
func TestProbe_PortRefusesRealJoinRequests(t *testing.T) {
	a, b := newGroupEnv(t)
	a.mgr.StartProbeListener()

	rec, err := a.mgr.Create("open room", KindPublic, nil)
	require.NoError(t, err, "Create")

	// A well-formed join request — Probe unset — straight at the
	// describe port.
	req := NewJoinRequest(rec.ID, b.pk, "let me in", 0)
	body, err := encodeJoinRequest(req)
	require.NoError(t, err, "encodeJoinRequest")

	ctx, cancel := probeCtxFor(t)
	defer cancel()
	stream, err := b.dmsgC.DialStream(ctx, dmsg.Addr{PK: a.pk, Port: ProbePort})
	require.NoError(t, err, "dial describe port")
	defer stream.Close() //nolint:errcheck

	require.NoError(t, stream.SetWriteDeadline(time.Now().Add(30*time.Second)))
	require.NoError(t, message.WriteFrame(stream, body), "write join frame")
	require.NoError(t, stream.SetReadDeadline(time.Now().Add(30*time.Second)))
	frame, err := message.ReadFrame(stream)
	require.NoError(t, err, "read answer")
	resp, err := decodeJoinResponse(frame)
	require.NoError(t, err, "decode answer")

	require.Equal(t, JoinStatusUnavailable, resp.Status,
		"the describe port must refuse to decide a join")
	require.NotEqual(t, JoinStatusAdmitted, resp.Status)

	// And nothing was admitted behind our back.
	owner, ok, err := a.mgr.Get(rec.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotContains(t, owner.Members, b.pk, "the describe port admitted a member")
	reqs, err := a.mgr.PendingJoins(rec.ID)
	require.NoError(t, err)
	require.Empty(t, reqs, "the describe port queued a request")
}

// Probing a group we already hold must not depend on the host being
// reachable: the store already knows everything the answer contains.
func TestProbe_LocalRecordAnswersWithoutHost(t *testing.T) {
	a, b := newGroupEnv(t)
	a.mgr.StartProbeListener()

	rec, err := a.mgr.Create("open room", KindPublic, nil)
	require.NoError(t, err, "Create")

	ctx, cancel := probeCtxFor(t)
	defer cancel()
	_, err = b.mgr.JoinByAddress(ctx, a.pk, rec.ID)
	require.NoError(t, err, "JoinByAddress")

	// Take the host's describe listener away; B is a member and should
	// still be able to describe its own group.
	a.mgr.StopProbeListener()

	d, err := b.mgr.ProbeGroup(ctx, a.pk, rec.ID)
	require.NoError(t, err, "a member must describe its own group locally")
	require.Equal(t, rec.ID, d.ID)
	require.Equal(t, KindPublic, d.Kind)
	require.Equal(t, rec.Port, d.Port)
}

// A host that does not serve the describe port at all — an older build —
// must surface as retryable-unreachable, which is what tells a UI to
// suggest an invite link instead of claiming the group does not exist.
func TestProbe_HostWithoutListenerIsUnreachable(t *testing.T) {
	a, b := newGroupEnv(t)
	// Deliberately no StartProbeListener on A.

	rec, err := a.mgr.Create("open room", KindPublic, nil)
	require.NoError(t, err, "Create")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = b.mgr.ProbeGroup(ctx, a.pk, rec.ID)
	require.Error(t, err, "a host with no describe listener cannot answer")
	require.NotErrorIs(t, err, ErrProbeNoSuchGroup,
		"an unreachable host must not be reported as a wrong address")
}
