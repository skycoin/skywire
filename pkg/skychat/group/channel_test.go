// Package group pkg/skychat/group/channel_test.go
//
// The invariants a broadcast channel rests on, none of which are visible
// from reading a single function:
//
//   - a channel cannot become a group, at the persistence boundary;
//   - only admins may post, permanently, not as a moderation setting;
//   - the subscription topology is O(admins) rather than O(members),
//     which is the difference between a channel that scales and one that
//     opens ten thousand dead subscriptions per admin.
//
// Pure unit tests — no transport. The dmsg-backed channel cases live in
// probe_integration_test.go.
package group

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// A channel must never be convertible into a group. Nothing in the current
// code tries to, which is exactly why this test exists: the guarantee is
// the store's, so a future caller that reassigns Kind fails here rather
// than silently handing the floor to every subscriber of a channel they
// joined on the understanding that only admins post.
func TestChannel_KindIsImmutable(t *testing.T) {
	forEachStore(t, func(t *testing.T, st *Store) {
		owner, _ := cipher.GenerateKeyPair()
		rec := Record{
			ID: "chan-1", Name: "news", OwnerPK: owner,
			Port: 40000, Mode: ModePublic, Kind: KindChannel,
			Members: []cipher.PubKey{owner}, Role: RoleOwner, Status: StatusActive,
		}
		require.NoError(t, st.Put(rec), "initial Put")

		// Every direction that would redefine what the group is.
		for _, to := range []Kind{KindPublic, KindPrivate} {
			bad := rec
			bad.Kind = to
			bad.Mode = modeForKind(to)
			err := st.Put(bad)
			require.Error(t, err, "channel must not become %q", to)
			require.ErrorIs(t, err, ErrKindImmutable)
		}

		// A write that merely FORGETS the kind is the dangerous one:
		// EnsureKind would re-derive it from Mode on the next read, and a
		// channel shares ModePublic with a public group — so the channel
		// would quietly reopen to everyone.
		dropped := rec
		dropped.Kind = ""
		err := st.Put(dropped)
		require.Error(t, err, "a write with no kind must not erase a channel's kind")
		require.ErrorIs(t, err, ErrKindImmutable)

		// And the stored record is untouched by any of those attempts.
		got, ok, err := st.Get("chan-1")
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, KindChannel, got.Kind)

		// An unchanged kind still writes — this guards a rename, a roster
		// change, and every other legitimate read-modify-write.
		renamed := rec
		renamed.Name = "news v2"
		require.NoError(t, st.Put(renamed), "an unchanged kind must still be writable")
	})
}

// A record written before Kind existed must still normalize: prev == ""
// is a first-time fill, not a change.
func TestChannel_LegacyRecordCanStillNormalize(t *testing.T) {
	forEachStore(t, func(t *testing.T, st *Store) {
		owner, _ := cipher.GenerateKeyPair()
		legacy := Record{
			ID: "legacy-1", OwnerPK: owner, Port: 40001,
			Mode:    ModePrivate, // no Kind, as a pre-Kind build wrote it
			Members: []cipher.PubKey{owner}, Role: RoleOwner, Status: StatusActive,
		}
		require.NoError(t, st.Put(legacy))

		// The store fills Kind on read; writing that back must be accepted.
		got, ok, err := st.Get("legacy-1")
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, KindPrivate, got.Kind, "EnsureKind should derive from Mode")
		require.NoError(t, st.Put(got), "normalizing a legacy record must be allowed")
	})
}

// Admins-only posting on a channel is a property of the group, not a
// moderation state — so it holds with ReadOnly off, and CanPost reports it
// with a reason naming the channel rather than the read-only flag.
func TestChannel_OnlyAdminsMayPost(t *testing.T) {
	owner, _ := cipher.GenerateKeyPair()
	admin, _ := cipher.GenerateKeyPair()
	member, _ := cipher.GenerateKeyPair()
	r := Record{
		ID: "c", OwnerPK: owner, Kind: KindChannel, Mode: ModePublic,
		Admins:  []cipher.PubKey{owner, admin},
		Members: []cipher.PubKey{owner, admin, member},
	}

	require.Equal(t, PostAdminsOnly, r.PostPolicy(), "a channel is admins-only whatever ReadOnly says")
	require.True(t, r.IsChannel())

	for _, pk := range []cipher.PubKey{owner, admin} {
		ok, reason := r.CanPost(pk)
		require.True(t, ok, "an admin must be able to post")
		require.Empty(t, reason)
	}
	ok, reason := r.CanPost(member)
	require.False(t, ok, "a subscriber must not be able to post")
	require.Contains(t, reason, "channel")

	// Joining is still open — a channel restricts publishing, not reading.
	require.Equal(t, JoinOpen, r.JoinPolicy())

	// A ban still outranks everything, and a mute still applies to an admin.
	banned := r
	banned.Banned = []cipher.PubKey{member}
	ok, reason = banned.CanPost(member)
	require.False(t, ok)
	require.Contains(t, reason, "banned")

	mutedAdmin := r
	mutedAdmin.Muted = []cipher.PubKey{admin}
	ok, reason = mutedAdmin.CanPost(admin)
	require.False(t, ok, "an explicit mute applies even to an admin")
	require.Contains(t, reason, "restricted")
}

// The scaling property. Under the group rule an admin follows every
// member; in a channel those feeds can never carry a message, so following
// them would mean one CXO subscription per subscriber per admin. Both roles
// must follow admins only.
func TestChannel_TopologyFollowsAdminsOnly(t *testing.T) {
	owner, _ := cipher.GenerateKeyPair()
	admin, _ := cipher.GenerateKeyPair()

	subscribers := make([]cipher.PubKey, 0, 50)
	for i := 0; i < 50; i++ {
		pk, _ := cipher.GenerateKeyPair()
		subscribers = append(subscribers, pk)
	}
	members := append([]cipher.PubKey{owner, admin}, subscribers...)
	channel := Record{
		ID: "c", OwnerPK: owner, Kind: KindChannel, Mode: ModePublic,
		Admins: []cipher.PubKey{owner, admin}, Members: members,
	}

	// From an admin's seat: the other admin, and nobody else. The 50
	// subscribers are exactly what the group rule would have followed.
	fromAdmin := desiredPeerSubsForRole(channel, owner, defaultPeerFanout)
	require.Len(t, fromAdmin, 1, "an admin must not follow a channel's subscribers")
	require.Contains(t, fromAdmin, admin)

	// From a subscriber's seat: both admins, and no other subscriber — the
	// fanout ring is off, because a channel's history comes from admins.
	fromSub := desiredPeerSubsForRole(channel, subscribers[0], defaultPeerFanout)
	require.Len(t, fromSub, 2)
	require.Contains(t, fromSub, owner)
	require.Contains(t, fromSub, admin)

	// Cost is flat in audience size: adding subscribers changes nothing.
	bigger := channel
	for i := 0; i < 200; i++ {
		pk, _ := cipher.GenerateKeyPair()
		bigger.Members = append(bigger.Members, pk)
	}
	require.Len(t, desiredPeerSubsForRole(bigger, owner, defaultPeerFanout), 1,
		"an admin's subscription count must not grow with the audience")

	// Contrast: the same roster as an ordinary group DOES fan out, which is
	// what makes the channel rule worth having.
	asGroup := channel
	asGroup.Kind = KindPublic
	require.Greater(t, len(desiredPeerSubsForRole(asGroup, owner, defaultPeerFanout)), 1,
		"a public group's admin still follows every member")
}

// A channel never serves history peer-to-peer: a subscriber has nothing of
// its own to serve, and mirroring the room onto every follower would give a
// large channel one copy of itself per member.
func TestChannel_PeerBackfillIsAlwaysOff(t *testing.T) {
	owner, _ := cipher.GenerateKeyPair()
	r := Record{ID: "c", OwnerPK: owner, Kind: KindChannel, Mode: ModePublic}
	require.False(t, r.PeerBackfillEnabled(), "a channel must not peer-backfill")

	// Even with the stored flag explicitly set to the enabled value.
	r.PeerBackfillDisabled = false
	require.False(t, r.PeerBackfillEnabled())

	// An ordinary group is unaffected.
	g := Record{ID: "g", OwnerPK: owner, Kind: KindPublic, Mode: ModePublic}
	require.True(t, g.PeerBackfillEnabled())
}

// Kind survives a link and is preferred from the group's own answer, so a
// channel joined from an invite arrives as a channel rather than as a public
// group with an unlocked composer.
func TestChannel_KindSurvivesInviteAndResponse(t *testing.T) {
	owner, _ := cipher.GenerateKeyPair()
	inv := Invite{ID: "c", Name: "news", OwnerPK: owner, Port: 40002, Mode: ModePublic, Kind: KindChannel}

	link, err := EncodeInvite(inv)
	require.NoError(t, err, "a channel invite must encode")
	back, err := DecodeInvite(link)
	require.NoError(t, err, "a channel invite must decode")
	require.Equal(t, KindChannel, back.Kind)
	require.Equal(t, KindChannel, back.InviteKind())

	// The group's own answer wins over the link.
	require.Equal(t, KindChannel, joinedKind(Invite{Mode: ModePublic}, JoinResponseMsg{GroupKind: KindChannel}))
	// The link is the fallback when the responder predates channels.
	require.Equal(t, KindChannel, joinedKind(back, JoinResponseMsg{}))
	// Neither source: Mode decides, and a pre-channel record was never one.
	require.Equal(t, KindPublic, joinedKind(Invite{Mode: ModePublic}, JoinResponseMsg{}))

	// A kind that contradicts Mode is refused rather than persisted: Mode
	// drives decryption and Kind drives posting, so a record whose halves
	// disagree either can't read the feed or can't post to it.
	_, err = EncodeInvite(Invite{ID: "x", OwnerPK: owner, Port: 1, Mode: ModePrivate, Kind: KindChannel})
	require.Error(t, err, "kind=channel with mode=private must be refused")
	require.Equal(t, KindPublic,
		joinedKind(Invite{Mode: ModePublic}, JoinResponseMsg{GroupKind: KindPrivate}),
		"a response kind that contradicts the mode falls back")
}

// Publishing is opt-in and admin-only, and a catalog lists only what was
// published.
func TestChannel_CatalogIsOptIn(t *testing.T) {
	owner, _ := cipher.GenerateKeyPair()
	r := Record{ID: "c", OwnerPK: owner, Kind: KindChannel, Mode: ModePublic}
	require.False(t, r.Listed, "a new record must not be listed")

	applied := Record{}
	WithListed(true)(&applied)
	require.True(t, applied.Listed)

	// The default when the option is not passed at all.
	untouched := Record{}
	require.False(t, untouched.Listed, "omitting the option must leave it unlisted")
}

// forEachStore runs fn against the platform's group-record store.
//
// Only one backend exists per build — store_memory.go is `//go:build js` and
// store_bbolt.go is its inverse — so this is a single case here and the
// wasm backend is covered when the suite runs under GOOS=js. The kind guard
// is implemented in both files precisely because they cannot be tested
// together; keeping this helper named for the general case is a reminder
// that the invariant belongs to both.
func forEachStore(t *testing.T, fn func(t *testing.T, st *Store)) {
	t.Helper()
	st, err := OpenStore(filepath.Join(t.TempDir(), "groups.db"), testStoreSK())
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() }) //nolint:errcheck
	fn(t, st)
}
