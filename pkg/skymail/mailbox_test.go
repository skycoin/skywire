package skymail

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skymailbridge"
)

// pkAddr is the remote address of a test connection: the PK that
// dialed, as the skynet and dmsg transports authenticate it.
type pkAddr struct{ pk cipher.PubKey }

func (a pkAddr) Network() string { return "test" }
func (a pkAddr) String() string  { return a.pk.Hex() }

type pkConn struct {
	net.Conn
	remote cipher.PubKey
}

func (c pkConn) RemoteAddr() net.Addr { return pkAddr{c.remote} }

func peerOf(c net.Conn) (cipher.PubKey, bool) {
	a, ok := c.RemoteAddr().(pkAddr)
	return a.pk, ok
}

// chanListener hands out the server ends of dialed pipes.
type chanListener struct {
	ch   chan net.Conn
	once sync.Once
	done chan struct{}
}

func newChanListener() *chanListener {
	return &chanListener{ch: make(chan net.Conn), done: make(chan struct{})}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *chanListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *chanListener) Addr() net.Addr { return pkAddr{} }

// net is a set of visors: each PK's port 25 is a mailbox listener.
type testNet struct {
	mu   sync.Mutex
	lis  map[cipher.PubKey]*chanListener
	down map[cipher.PubKey]bool
}

// dialerFor returns the dialer visor `from` uses.
func (n *testNet) dialerFor(from cipher.PubKey) skymailbridge.Dialer { return netDialer{n, from} }

type netDialer struct {
	n    *testNet
	from cipher.PubKey
}

func (d netDialer) Dial(ctx context.Context, peer cipher.PubKey, _ uint16) (net.Conn, error) {
	d.n.mu.Lock()
	l, ok := d.n.lis[peer]
	down := d.n.down[peer]
	d.n.mu.Unlock()
	if !ok || down {
		return nil, errors.New("peer unreachable")
	}
	client, server := net.Pipe()
	select {
	case l.ch <- pkConn{Conn: server, remote: d.from}:
		return client, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type visor struct {
	pk cipher.PubKey
	mb *Mailbox
}

func newTestNet() *testNet {
	return &testNet{lis: map[cipher.PubKey]*chanListener{}, down: map[cipher.PubKey]bool{}}
}

// addVisor opens a mailbox for a fresh PK and serves it. It is
// reachable over .skynet only; .dmsg has no dialer.
func (n *testNet) addVisor(t *testing.T) *visor {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	mb, err := Open(Config{
		Dir:     t.TempDir(),
		PK:      pk,
		PeerPK:  peerOf,
		Dialers: map[string]skymailbridge.Dialer{".skynet": n.dialerFor(pk)},
	})
	require.NoError(t, err)
	l := newChanListener()
	n.mu.Lock()
	n.lis[pk] = l
	n.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = mb.Serve(ctx, l); close(done) }() //nolint:errcheck
	t.Cleanup(func() { cancel(); <-done })
	return &visor{pk: pk, mb: mb}
}

func addr(local string, pk cipher.PubKey) string {
	return local + "@" + pk.DNSLabel() + ".skynet"
}

func TestSendDeliversVerbatimWithTheVerifiedSender(t *testing.T) {
	n := newTestNet()
	a, b := n.addVisor(t), n.addVisor(t)

	// Non-ASCII, a line past SMTP's 998-octet limit, and lines SMTP
	// dot-stuffs: all must arrive exactly as written.
	body := "héllo over skynet\n.leading dot\n.\n" + strings.Repeat("x", 2000) + "\nend"
	res, err := a.mb.Send(context.Background(), Outgoing{
		From: "alice", To: []string{addr("bob", b.pk)}, Subject: "grüße", Body: body,
	})
	require.NoError(t, err)
	require.Len(t, res.Recipients, 1)
	require.Empty(t, res.Recipients[0].Err)
	require.NotEmpty(t, res.ID, "a delivered message keeps a Sent copy")

	inbox, err := b.mb.List(FolderInbox)
	require.NoError(t, err)
	require.Len(t, inbox, 1)
	got := inbox[0]
	require.Equal(t, "grüße", got.Subject)
	require.Equal(t, addr("alice", a.pk), got.From)
	require.Equal(t, a.pk.Hex(), got.PeerPK)
	require.True(t, got.FromVerified, "From names the PK that delivered it")
	require.False(t, got.Seen)

	raw, err := b.mb.Raw(FolderInbox, got.ID)
	require.NoError(t, err)
	r, err := Render(raw)
	require.NoError(t, err)
	require.Equal(t, strings.ReplaceAll(body, "\n", "\r\n")+"\r\n", r.Text)
	require.True(t, r.Verified)

	sent, err := a.mb.List(FolderSent)
	require.NoError(t, err)
	require.Len(t, sent, 1)
	require.True(t, sent[0].Seen)

	require.NoError(t, b.mb.MarkSeen(FolderInbox, got.ID))
	inbox, err = b.mb.List(FolderInbox)
	require.NoError(t, err)
	require.True(t, inbox[0].Seen)
	require.NoError(t, b.mb.Delete(FolderInbox, got.ID))
	inbox, err = b.mb.List(FolderInbox)
	require.NoError(t, err)
	require.Empty(t, inbox)
}

func TestWhitelistRefusesOtherPKs(t *testing.T) {
	n := newTestNet()
	a, b, c := n.addVisor(t), n.addVisor(t), n.addVisor(t)
	require.NoError(t, b.mb.SetWhitelist([]cipher.PubKey{c.pk}))

	res, err := a.mb.Send(context.Background(), Outgoing{To: []string{addr("bob", b.pk)}, Subject: "hi", Body: "x"})
	require.Error(t, err)
	require.Contains(t, res.Recipients[0].Err, "not whitelisted")
	sent, _ := a.mb.List(FolderSent) //nolint:errcheck
	require.Empty(t, sent, "nothing delivered, nothing filed as sent")

	_, err = c.mb.Send(context.Background(), Outgoing{To: []string{addr("bob", b.pk)}, Subject: "hi", Body: "x"})
	require.NoError(t, err)

	require.NoError(t, b.mb.SetWhitelist(nil))
	_, err = a.mb.Send(context.Background(), Outgoing{To: []string{addr("bob", b.pk)}, Subject: "hi", Body: "x"})
	require.NoError(t, err, "an empty whitelist accepts everyone")
	inbox, _ := b.mb.List(FolderInbox) //nolint:errcheck
	require.Len(t, inbox, 2)
}

// smtpAs speaks raw SMTP to `to` as visor `as`.
func smtpAs(t *testing.T, n *testNet, as, to cipher.PubKey, from string, rcpts []string, msg string) error {
	t.Helper()
	conn, err := n.dialerFor(as).Dial(context.Background(), to, 25)
	require.NoError(t, err)
	cl, err := smtp.NewClient(conn, "peer")
	require.NoError(t, err)
	defer cl.Close() //nolint:errcheck
	if err := cl.Mail(from); err != nil {
		return err
	}
	for _, r := range rcpts {
		if err := cl.Rcpt(r); err != nil {
			return err
		}
	}
	w, err := cl.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	return w.Close()
}

func TestMailboxIsNotARelay(t *testing.T) {
	n := newTestNet()
	a, b, c := n.addVisor(t), n.addVisor(t), n.addVisor(t)
	err := smtpAs(t, n, a.pk, b.pk, addr("a", a.pk), []string{addr("c", c.pk)}, "Subject: x\r\n\r\nx\r\n")
	require.ErrorContains(t, err, "not a mailbox of this visor")
	err = smtpAs(t, n, a.pk, b.pk, addr("a", a.pk), []string{"someone@example.com"}, "Subject: x\r\n\r\nx\r\n")
	require.ErrorContains(t, err, "not a mailbox of this visor")
}

func TestForgedFromAndForgedPeerHeaderAreNotVerified(t *testing.T) {
	n := newTestNet()
	a, b, c := n.addVisor(t), n.addVisor(t), n.addVisor(t)
	msg := fmt.Sprintf("%s: %s\r\nFrom: %s\r\nSubject: forged\r\n\r\nx\r\n", PeerHeader, c.pk.Hex(), addr("c", c.pk))
	require.NoError(t, smtpAs(t, n, a.pk, b.pk, addr("c", c.pk), []string{addr("b", b.pk)}, msg))

	inbox, err := b.mb.List(FolderInbox)
	require.NoError(t, err)
	require.Len(t, inbox, 1)
	require.Equal(t, a.pk.Hex(), inbox[0].PeerPK, "the receiver's own stamp wins over one the sender wrote")
	require.False(t, inbox[0].FromVerified, "From claims C but A delivered it")
}

func TestSendReportsEachRecipientAndDeliversWhatItCan(t *testing.T) {
	n := newTestNet()
	a, b, c := n.addVisor(t), n.addVisor(t), n.addVisor(t)
	n.down[c.pk] = true

	res, err := a.mb.Send(context.Background(), Outgoing{
		To:      []string{addr("b", b.pk), addr("c", c.pk), "x@example.com", "d@" + b.pk.DNSLabel() + ".dmsg"},
		Cc:      []string{addr("me", a.pk)},
		Subject: "multi", Body: "x",
	})
	require.NoError(t, err, "delivered to some")
	errs := map[string]string{}
	for _, r := range res.Recipients {
		errs[r.Rcpt] = r.Err
	}
	require.Empty(t, errs[addr("b", b.pk)])
	require.Empty(t, errs[addr("me", a.pk)], "own PK is filed locally")
	require.Contains(t, errs[addr("c", c.pk)], "unreachable")
	require.Contains(t, errs["x@example.com"], "not a skywire address")
	require.Contains(t, errs["d@"+b.pk.DNSLabel()+".dmsg"], "no route")

	own, _ := a.mb.List(FolderInbox) //nolint:errcheck
	require.Len(t, own, 1)
	require.True(t, own[0].FromVerified)
}

func TestSendRejectsHeaderInjection(t *testing.T) {
	n := newTestNet()
	a, b := n.addVisor(t), n.addVisor(t)
	_, err := a.mb.Send(context.Background(), Outgoing{
		To: []string{addr("b", b.pk) + "\r\nBcc: x@example.com"}, Subject: "x", Body: "x",
	})
	require.Error(t, err)
	_, err = a.mb.Send(context.Background(), Outgoing{From: "me\r\nX-Evil: 1", To: []string{addr("b", b.pk)}, Body: "x"})
	require.Error(t, err)
}

func TestSendToAnOfflineVisorFailsNow(t *testing.T) {
	n := newTestNet()
	a, b := n.addVisor(t), n.addVisor(t)
	n.down[b.pk] = true
	start := time.Now()
	_, err := a.mb.Send(context.Background(), Outgoing{To: []string{addr("b", b.pk)}, Body: "x"})
	require.Error(t, err)
	require.Less(t, time.Since(start), 5*time.Second)
}

func TestMessageIDsCannotLeaveTheFolder(t *testing.T) {
	n := newTestNet()
	a := n.addVisor(t)
	for _, id := range []string{"../x", "..", "a/b", "", ".."} {
		_, err := a.mb.Raw(FolderInbox, id)
		require.ErrorIs(t, err, ErrNotFound, id)
		require.ErrorIs(t, a.mb.Delete(FolderInbox, id), ErrNotFound, id)
	}
	_, err := a.mb.List("../../etc")
	require.Error(t, err)
}

func TestWhitelistPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	pk, _ := cipher.GenerateKeyPair()
	allowed, _ := cipher.GenerateKeyPair()
	mb, err := Open(Config{Dir: dir, PK: pk})
	require.NoError(t, err)
	require.Empty(t, mb.Whitelist())
	require.NoError(t, mb.SetWhitelist([]cipher.PubKey{allowed}))

	again, err := Open(Config{Dir: dir, PK: pk})
	require.NoError(t, err)
	require.Equal(t, []cipher.PubKey{allowed}, again.Whitelist())

	require.NoError(t, again.SetWhitelist(nil))
	again, err = Open(Config{Dir: dir, PK: pk})
	require.NoError(t, err)
	require.Empty(t, again.Whitelist(), "clearing it opens the mailbox to everyone again")
}
