package skymail

import (
	"context"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skymailbridge"
)

const sealedMsg = "From: a\r\nSubject: sealed\r\n\r\nonly for b\r\n"

func TestSealedEnvelopeRoundTrips(t *testing.T) {
	spk, ssk := cipher.GenerateKeyPair()
	rpk, rsk := cipher.GenerateKeyPair()
	for _, msg := range []string{"", sealedMsg, strings.Repeat("é\r\n", 50000)} {
		env, err := Seal([]byte(msg), ssk, rpk)
		require.NoError(t, err)
		require.Equal(t, SealedSize(int64(len(msg))), int64(len(env)), "SealedSize is exact")
		require.True(t, IsSealed(env))
		require.NotContains(t, string(env), "only for b")
		for _, line := range strings.Split(string(env), "\r\n") {
			require.LessOrEqual(t, len(line), 998, "RFC 5322 line limit")
		}
		got, signer, err := OpenSealed(env, rsk)
		require.NoError(t, err)
		require.Equal(t, msg, string(got))
		require.Equal(t, spk, signer)
	}
	require.False(t, IsSealed([]byte(sealedMsg)))
	_, _, err := OpenSealed([]byte(sealedMsg), rsk)
	require.ErrorIs(t, err, ErrNotSealed)
}

func TestSealedEnvelopeOpensOnlyForItsRecipient(t *testing.T) {
	_, ssk := cipher.GenerateKeyPair()
	rpk, _ := cipher.GenerateKeyPair()
	_, other := cipher.GenerateKeyPair()
	env, err := Seal([]byte(sealedMsg), ssk, rpk)
	require.NoError(t, err)
	_, _, err = OpenSealed(env, other)
	require.ErrorIs(t, err, ErrNotForUs)
}

func TestTamperedEnvelopeDoesNotOpen(t *testing.T) {
	_, ssk := cipher.GenerateKeyPair()
	rpk, rsk := cipher.GenerateKeyPair()
	env, err := Seal([]byte(sealedMsg), ssk, rpk)
	require.NoError(t, err)
	// Change one base64 character well past the recipient PK.
	i := len(sealedHeader) + 200
	flip := byte('A')
	if env[i] == 'A' {
		flip = 'B'
	}
	bad := append([]byte{}, env...)
	bad[i] = flip
	_, _, err = OpenSealed(bad, rsk)
	require.ErrorIs(t, err, ErrSealBroken)
}

// TestResealedSignatureIsRejected: B opens a message A signed for B and
// seals it, signature and all, to C. C must not take it as A's mail to C.
func TestResealedSignatureIsRejected(t *testing.T) {
	apk, ask := cipher.GenerateKeyPair()
	bpk, bsk := cipher.GenerateKeyPair()
	cpk, csk := cipher.GenerateKeyPair()
	sig, err := cipher.SignPayload(sealedSigned(bpk, []byte(sealedMsg)), ask)
	require.NoError(t, err)
	toB, err := sealRaw(apk, sig, []byte(sealedMsg), bpk)
	require.NoError(t, err)
	_, signer, err := OpenSealed(toB, bsk)
	require.NoError(t, err)
	require.Equal(t, apk, signer)

	toC, err := sealRaw(apk, sig, []byte(sealedMsg), cpk)
	require.NoError(t, err)
	_, _, err = OpenSealed(toC, csk)
	require.ErrorIs(t, err, ErrBadSealSign)
}

// relayAs delivers body to `to` over a connection from `via`, the way a
// visor carrying someone else's envelope would.
func relayAs(n *testNet, via, to cipher.PubKey, from string, body []byte) error {
	rcpt := addr("b", to)
	return skymailbridge.Relay(context.Background(), n.dialerFor(via),
		skymailbridge.Config{Suffix: ".skynet", Mode: "b", HeloName: "relay", RemotePort: 25},
		from, []skymailbridge.Recipient{{Original: rcpt, Forward: rcpt, PeerPK: to}}, body, logrus.New())
}

func TestRelayedEnvelopeIsFiledUnderItsSigner(t *testing.T) {
	n := newTestNet()
	a, b, c := n.addVisor(t), n.addVisor(t), n.addVisor(t)
	msg := "From: " + addr("alice", a.pk) + "\r\nSubject: via c\r\n\r\nhello\r\n"
	env, err := Seal([]byte(msg), a.sk, b.pk)
	require.NoError(t, err)

	require.NoError(t, relayAs(n, c.pk, b.pk, addr("alice", a.pk), env))
	inbox, err := b.mb.List(FolderInbox)
	require.NoError(t, err)
	require.Len(t, inbox, 1)
	require.Equal(t, "via c", inbox[0].Subject)
	require.Equal(t, a.pk.Hex(), inbox[0].PeerPK, "filed under the signer, not the relay")
	require.True(t, inbox[0].FromVerified)
	require.Equal(t, c.pk.Hex(), inbox[0].RelayPK)

	raw, err := b.mb.Raw(FolderInbox, inbox[0].ID)
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(string(raw), msg), "the message is stored as A wrote it")
	require.Contains(t, string(raw), "skymail sealed")
	r, err := Render(raw)
	require.NoError(t, err)
	require.Equal(t, c.pk.Hex(), r.RelayPK)
	require.True(t, r.Verified)

	// A delivering its own envelope is not a relay.
	require.NoError(t, relayAs(n, a.pk, b.pk, addr("alice", a.pk), env))
	inbox, _ = b.mb.List(FolderInbox) //nolint:errcheck
	require.Len(t, inbox, 2)
	require.Empty(t, inbox[0].RelayPK)
}

func TestWhitelistAppliesToTheSignerOfASealedEnvelope(t *testing.T) {
	n := newTestNet()
	a, b, c, d := n.addVisor(t), n.addVisor(t), n.addVisor(t), n.addVisor(t)
	require.NoError(t, b.mb.SetWhitelist([]cipher.PubKey{a.pk}))
	fromA := addr("alice", a.pk)

	env, err := Seal([]byte(sealedMsg), a.sk, b.pk)
	require.NoError(t, err)
	require.NoError(t, relayAs(n, c.pk, b.pk, fromA, env), "C carries A's mail")

	err = relayAs(n, c.pk, b.pk, fromA, []byte(sealedMsg))
	require.ErrorContains(t, err, "not whitelisted", "unsealed mail that only claims to be A's")

	byD, err := Seal([]byte(sealedMsg), d.sk, b.pk)
	require.NoError(t, err)
	err = relayAs(n, c.pk, b.pk, fromA, byD)
	require.ErrorContains(t, err, "not whitelisted", "D signed it, whatever MAIL FROM says")

	err = relayAs(n, c.pk, b.pk, addr("c", c.pk), env)
	require.ErrorContains(t, err, "not whitelisted", "refused at RCPT")

	inbox, _ := b.mb.List(FolderInbox) //nolint:errcheck
	require.Len(t, inbox, 1)
}

func TestEnvelopeForAnotherVisorIsRefused(t *testing.T) {
	n := newTestNet()
	a, b, c := n.addVisor(t), n.addVisor(t), n.addVisor(t)
	env, err := Seal([]byte(sealedMsg), a.sk, c.pk)
	require.NoError(t, err)
	err = relayAs(n, c.pk, b.pk, addr("alice", a.pk), env)
	require.ErrorContains(t, err, "554")
	require.ErrorContains(t, err, "another visor")
}

func TestSealedEnvelopeMeetsTheMessageLimit(t *testing.T) {
	n := newTestNet()
	a, b, c := n.addVisor(t), n.addVisor(t), n.addVisor(t)
	b.mb.SetLimits(Limits{MaxMessageSize: 4096})
	fits, err := Seal([]byte(strings.Repeat("x", 4000)), a.sk, b.pk)
	require.NoError(t, err)
	require.Greater(t, len(fits), 4096)
	require.NoError(t, relayAs(n, c.pk, b.pk, addr("a", a.pk), fits), "the limit is on the message, not its envelope")

	big, err := Seal([]byte(strings.Repeat("x", 5000)), a.sk, b.pk)
	require.NoError(t, err)
	require.ErrorContains(t, relayAs(n, c.pk, b.pk, addr("a", a.pk), big), "552")

	err = relayAs(n, a.pk, b.pk, addr("a", a.pk), []byte(strings.Repeat("y", 5000)))
	require.ErrorContains(t, err, "552", "plain mail still meets the plain limit")
}

func TestOnlyTheMailboxCanNameARelay(t *testing.T) {
	n := newTestNet()
	a, b := n.addVisor(t), n.addVisor(t)
	_, err := a.mb.Send(context.Background(), Outgoing{To: []string{addr("b", b.pk)}, Subject: "forged", Body: "x"})
	require.NoError(t, err)
	forged := RelayHeader + ": " + a.pk.Hex() + "\r\nSubject: forged\r\n\r\nx\r\n"
	require.NoError(t, smtpAs(t, n, a.pk, b.pk, addr("a", a.pk), []string{addr("b", b.pk)}, forged))
	inbox, err := b.mb.List(FolderInbox)
	require.NoError(t, err)
	require.Len(t, inbox, 2)
	for _, m := range inbox {
		require.Empty(t, m.RelayPK)
	}
}

func TestEHLOOffersSealedOnlyWithAKey(t *testing.T) {
	n := newTestNet()
	a, b := n.addVisor(t), n.addVisor(t)
	c := n.addVisor(t, func(c *Config) { c.Sealer = nil })

	ehlo := func(to cipher.PubKey) string {
		tc, done := rawSession(t, n, a.pk, to)
		defer done()
		return cmd(t, tc, 250, "EHLO a")
	}
	require.Contains(t, ehlo(b.pk), SealedExtension)
	require.NotContains(t, ehlo(c.pk), SealedExtension)

	env, err := Seal([]byte(sealedMsg), a.sk, c.pk)
	require.NoError(t, err)
	require.NoError(t, relayAs(n, a.pk, c.pk, addr("a", a.pk), env))
	raw, err := c.mb.List(FolderInbox)
	require.NoError(t, err)
	require.Len(t, raw, 1)
	require.Empty(t, raw[0].Subject, "a mailbox without a key keeps the envelope as it came")
}
