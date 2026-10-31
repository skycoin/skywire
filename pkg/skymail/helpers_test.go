package skymail

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// pkSet is a PK list a test changes while mailboxes read it.
type pkSet struct {
	mu  sync.Mutex
	pks []cipher.PubKey
}

func (s *pkSet) set(pks ...cipher.PubKey) {
	s.mu.Lock()
	s.pks = pks
	s.mu.Unlock()
}

func (s *pkSet) list() []cipher.PubKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.pks)
}

func (s *pkSet) has(pk cipher.PubKey) bool { return slices.Contains(s.list(), pk) }

// startOutbox runs v's Outbox worker until the returned stop is called.
func startOutbox(t *testing.T, v *visor) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { v.mb.RunOutbox(ctx); close(done) }()
	var once sync.Once
	stop = func() { once.Do(func() { cancel(); <-done }) }
	t.Cleanup(stop)
	return stop
}

// helperNet is sender A with helper H and recipient B, B offline.
type helperNet struct {
	n                *testNet
	a, h, b          *visor
	helpers, relayOf *pkSet
}

func newHelperNet(t *testing.T, opts ...func(*Config)) *helperNet {
	hn := &helperNet{n: newTestNet(), helpers: &pkSet{}, relayOf: &pkSet{}}
	hn.a = hn.n.addVisor(t, func(c *Config) { c.Helpers = hn.helpers.list })
	hn.h = hn.n.addVisor(t, func(c *Config) { c.RelayFor = hn.relayOf.has })
	hn.b = hn.n.addVisor(t, opts...)
	hn.helpers.set(hn.h.pk)
	hn.relayOf.set(hn.a.pk)
	hn.n.setDown(hn.b, true)
	return hn
}

func queueOf(t *testing.T, v *visor) []Summary {
	t.Helper()
	l, err := v.mb.List(FolderOutbox)
	require.NoError(t, err)
	return l
}

func TestHelperDeliversWhileTheSenderIsAway(t *testing.T) {
	hn := newHelperNet(t)
	stopA := startOutbox(t, hn.a)
	startOutbox(t, hn.h)

	msg := Outgoing{From: "alice", To: []string{addr("bob", hn.b.pk)}, Subject: "while away", Body: "carried"}
	res, err := hn.a.mb.Send(context.Background(), msg)
	require.NoError(t, err)
	require.True(t, res.Recipients[0].Queued)
	waitFor(t, "A hands it to H", func() bool {
		q := queueOf(t, hn.a)
		return len(q) == 1 && slices.Contains(q[0].Queue.Handed, hn.h.pk.Hex())
	})
	carried := queueOf(t, hn.h)
	require.Len(t, carried, 1)
	require.Equal(t, hn.a.pk.Hex(), carried[0].Queue.CarriedFor)
	require.Equal(t, []string{addr("bob", hn.b.pk)}, carried[0].Queue.Pending)
	raw, err := hn.h.mb.Raw(FolderOutbox, carried[0].ID)
	require.NoError(t, err)
	require.True(t, IsSealed(raw))
	require.NotContains(t, string(raw), "carried", "H cannot read it")

	// A goes away, as a closed browser tab does. B comes back.
	stopA()
	hn.n.setDown(hn.a, true)
	hn.n.setDown(hn.b, false)
	require.NoError(t, hn.h.mb.Retry(""))
	waitFor(t, "H delivers to B", func() bool { return folderLen(t, hn.b, FolderInbox) == 1 })
	inbox, _ := hn.b.mb.List(FolderInbox) //nolint:errcheck
	require.Equal(t, "while away", inbox[0].Subject)
	require.Equal(t, hn.a.pk.Hex(), inbox[0].PeerPK)
	require.Equal(t, hn.h.pk.Hex(), inbox[0].RelayPK)
	require.True(t, inbox[0].FromVerified)
	waitFor(t, "H's queue empties", func() bool { return hn.h.mb.Queued() == 0 })
	require.Zero(t, folderLen(t, hn.h, FolderSent), "not H's mail to keep")
	require.Zero(t, folderLen(t, hn.h, FolderInbox), "no bounce at H")

	// A comes back and delivers its own copy; B files it once.
	hn.n.setDown(hn.a, false)
	startOutbox(t, hn.a)
	require.NoError(t, hn.a.mb.Retry(""))
	waitFor(t, "A's queue empties", func() bool { return hn.a.mb.Queued() == 0 })
	require.Equal(t, 1, folderLen(t, hn.b, FolderInbox), "the repeat is not filed")
	require.Equal(t, 1, folderLen(t, hn.a, FolderSent))
}

func TestHelperCarriesOnlyForTheVisorsItRelaysFor(t *testing.T) {
	hn := newHelperNet(t)
	hn.relayOf.set()
	startOutbox(t, hn.a)
	_, err := hn.a.mb.Send(context.Background(), Outgoing{To: []string{addr("bob", hn.b.pk)}, Body: "x"})
	require.NoError(t, err)
	time.Sleep(300 * time.Millisecond)
	require.Empty(t, queueOf(t, hn.a)[0].Queue.Handed)
	require.Zero(t, hn.h.mb.Queued())

	// The refusal is at RCPT, before any body is sent.
	c := hn.n.addVisor(t)
	env, err := Seal([]byte(sealedMsg), c.sk, hn.b.pk)
	require.NoError(t, err)
	err = smtpAs(t, hn.n, c.pk, hn.h.pk, addr("c", c.pk), []string{addr("bob", hn.b.pk)}, string(env))
	require.ErrorContains(t, err, "not a mailbox of this visor")

	// Once H relays for A, the next retry hands it over.
	hn.relayOf.set(hn.a.pk)
	require.NoError(t, hn.a.mb.Retry(""))
	waitFor(t, "handed after H allows it", func() bool { return hn.h.mb.Queued() == 1 })
}

func TestCarriedMailIsOnlySealedMailForTheNamedVisor(t *testing.T) {
	hn := newHelperNet(t)
	plain := "Subject: plain\r\n\r\nnot sealed\r\n"
	err := smtpAs(t, hn.n, hn.a.pk, hn.h.pk, addr("a", hn.a.pk), []string{addr("bob", hn.b.pk)}, plain)
	require.ErrorContains(t, err, "only sealed mail")

	other := hn.n.addVisor(t)
	env, err := Seal([]byte(sealedMsg), hn.a.sk, other.pk)
	require.NoError(t, err)
	err = smtpAs(t, hn.n, hn.a.pk, hn.h.pk, addr("a", hn.a.pk), []string{addr("bob", hn.b.pk)}, string(env))
	require.ErrorContains(t, err, "sealed for another visor")

	err = smtpAs(t, hn.n, hn.a.pk, hn.h.pk, addr("a", hn.a.pk), []string{addr("bob", hn.b.pk), addr("x", other.pk)}, string(env))
	require.ErrorContains(t, err, "one recipient visor")
	require.Zero(t, hn.h.mb.Queued())
}

func TestCarriedMailWaitsForARecipientThatOpensIt(t *testing.T) {
	hn := newHelperNet(t, func(c *Config) { c.SK = cipher.SecKey{} })
	startOutbox(t, hn.a)
	startOutbox(t, hn.h)
	_, err := hn.a.mb.Send(context.Background(), Outgoing{To: []string{addr("bob", hn.b.pk)}, Body: "x"})
	require.NoError(t, err)
	waitFor(t, "handed", func() bool { return hn.h.mb.Queued() == 1 })

	hn.n.setDown(hn.b, false)
	require.NoError(t, hn.h.mb.Retry(""))
	waitFor(t, "H drops what B cannot open", func() bool { return hn.h.mb.Queued() == 0 })
	require.Zero(t, folderLen(t, hn.b, FolderInbox), "never filed as a blob")
	require.Zero(t, folderLen(t, hn.h, FolderInbox), "no bounce at H")
	require.Equal(t, 1, hn.a.mb.Queued(), "A still has its own copy")
	require.NoError(t, hn.a.mb.Retry(""))
	waitFor(t, "A delivers it itself", func() bool { return folderLen(t, hn.b, FolderInbox) == 1 })
}

func TestHandOffWaitsForAHelperThatIsAway(t *testing.T) {
	hn := newHelperNet(t)
	hn.n.setDown(hn.h, true)
	startOutbox(t, hn.a)
	_, err := hn.a.mb.Send(context.Background(), Outgoing{To: []string{addr("bob", hn.b.pk)}, Body: "x"})
	require.NoError(t, err)
	time.Sleep(300 * time.Millisecond)
	require.Empty(t, queueOf(t, hn.a)[0].Queue.Handed)

	hn.n.setDown(hn.h, false)
	require.NoError(t, hn.a.mb.Retry(""))
	waitFor(t, "handed once H is back", func() bool { return hn.h.mb.Queued() == 1 })
	require.Equal(t, []string{hn.h.pk.Hex()}, queueOf(t, hn.a)[0].Queue.Handed)

	// Survives a restart: the reopened Outbox does not hand it over again.
	re, err := Open(hn.a.mb.cfg)
	require.NoError(t, err)
	out, err := re.List(FolderOutbox)
	require.NoError(t, err)
	require.Equal(t, []string{hn.h.pk.Hex()}, out[0].Queue.Handed)
}

func TestARepeatedMessageIsFiledOnce(t *testing.T) {
	n := newTestNet()
	a, b := n.addVisor(t), n.addVisor(t)
	msg := "Message-ID: <once@x>\r\nSubject: once\r\n\r\nx\r\n"
	for range 2 {
		require.NoError(t, smtpAs(t, n, a.pk, b.pk, addr("a", a.pk), []string{addr("b", b.pk)}, msg))
	}
	require.Equal(t, 1, folderLen(t, b, FolderInbox))

	// The same Message-ID from someone else is someone else's message.
	c := n.addVisor(t)
	require.NoError(t, smtpAs(t, n, c.pk, b.pk, addr("c", c.pk), []string{addr("b", b.pk)}, msg))
	require.Equal(t, 2, folderLen(t, b, FolderInbox))

	re, err := Open(b.mb.cfg)
	require.NoError(t, err)
	require.False(t, re.seen.claim(seenKey(a.pk, []byte(msg)), time.Now()), "remembered across a restart")
	require.NoError(t, re.seen.prune(time.Now().Add(seenTTL+time.Hour)))
	re2, err := Open(b.mb.cfg)
	require.NoError(t, err)
	require.True(t, re2.seen.claim(seenKey(a.pk, []byte(msg)), time.Now()), "forgotten after seenTTL")
}

func TestSealedRecipientReadsTheEnvelopeWithoutOpeningIt(t *testing.T) {
	_, ssk := cipher.GenerateKeyPair()
	rpk, _ := cipher.GenerateKeyPair()
	env, err := Seal([]byte(sealedMsg), ssk, rpk)
	require.NoError(t, err)
	got, ok := SealedRecipient(env)
	require.True(t, ok)
	require.Equal(t, rpk, got)
	_, ok = SealedRecipient([]byte(sealedMsg))
	require.False(t, ok)
	_, ok = SealedRecipient([]byte(strings.SplitAfter(string(env), "\r\n\r\n")[0] + "AAAA\r\n"))
	require.False(t, ok)
}
