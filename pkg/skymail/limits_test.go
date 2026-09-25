package skymail

import (
	"bufio"
	"context"
	"fmt"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestLimitsDefaultAndDisable(t *testing.T) {
	require.Equal(t, Limits{DefaultMaxMessageSize, DefaultMaxTotalSize, DefaultMaxAge}, Limits{}.WithDefaults())
	l := Limits{MaxMessageSize: -1, MaxTotalSize: -1, MaxAge: -1}.WithDefaults()
	require.Negative(t, l.MaxMessageSize, "negative means no bound, not the default")
	require.Negative(t, l.MaxTotalSize)
	require.Negative(t, l.MaxAge)
}

// rawSession opens an SMTP conversation with b as visor a.
func rawSession(t *testing.T, n *testNet, a, b cipher.PubKey) (*textproto.Conn, func()) {
	t.Helper()
	c, err := n.dialerFor(a).Dial(context.Background(), b, 25)
	require.NoError(t, err)
	tc := textproto.NewConn(c)
	_, _, err = tc.ReadResponse(220)
	require.NoError(t, err)
	return tc, func() { _ = tc.Close() } //nolint:errcheck
}

func cmd(t *testing.T, tc *textproto.Conn, want int, format string, args ...any) string {
	t.Helper()
	id, err := tc.Cmd(format, args...)
	require.NoError(t, err)
	tc.StartResponse(id)
	defer tc.EndResponse(id)
	_, msg, err := tc.ReadResponse(want)
	require.NoError(t, err, "%s", fmt.Sprintf(format, args...))
	return msg
}

// TestOversizedMessageIsRefusedAndTheSessionSurvives: a body over the
// limit is read to its end and answered 552, and the same session then
// delivers a message that fits.
func TestOversizedMessageIsRefusedAndTheSessionSurvives(t *testing.T) {
	n := newTestNet()
	a, b := n.addVisor(t), n.addVisor(t)
	b.mb.SetLimits(Limits{MaxMessageSize: 4096})

	tc, done := rawSession(t, n, a.pk, b.pk)
	defer done()
	require.Contains(t, cmd(t, tc, 250, "EHLO a"), "SIZE 4096")
	cmd(t, tc, 552, "MAIL FROM:<x@y> SIZE=10000")

	cmd(t, tc, 250, "MAIL FROM:<%s>", addr("a", a.pk))
	cmd(t, tc, 250, "RCPT TO:<%s>", addr("b", b.pk))
	cmd(t, tc, 354, "DATA")
	w := bufio.NewWriter(tc.W)
	for i := 0; i < 200; i++ {
		fmt.Fprintf(w, "%s\r\n", strings.Repeat("x", 70)) //nolint:errcheck
	}
	require.NoError(t, w.Flush())
	cmd(t, tc, 552, ".")

	cmd(t, tc, 250, "MAIL FROM:<%s>", addr("a", a.pk))
	cmd(t, tc, 250, "RCPT TO:<%s>", addr("b", b.pk))
	cmd(t, tc, 354, "DATA")
	cmd(t, tc, 250, "Subject: small\r\n\r\nfits\r\n.")
	inbox, err := b.mb.List(FolderInbox)
	require.NoError(t, err)
	require.Len(t, inbox, 1)
	require.Equal(t, "small", inbox[0].Subject)
}

func TestFullMailboxRefusesUntilRoomIsMade(t *testing.T) {
	n := newTestNet()
	a, b := n.addVisor(t), n.addVisor(t)
	body := strings.Repeat("y", 1000)
	send := func() *SendResult {
		res, _ := a.mb.Send(context.Background(), Outgoing{To: []string{addr("b", b.pk)}, Body: body}) //nolint:errcheck
		return res
	}
	require.Empty(t, send().Recipients[0].Err)
	one, err := b.mb.Usage()
	require.NoError(t, err)
	quota := one*2 + one/2 // room for two, not three
	b.mb.SetLimits(Limits{MaxTotalSize: quota})
	require.Empty(t, send().Recipients[0].Err)
	full := send()
	require.Contains(t, full.Recipients[0].Err, "mailbox full")
	used, err := b.mb.Usage()
	require.NoError(t, err)
	require.LessOrEqual(t, used, quota, "the quota holds")

	inbox, err := b.mb.List(FolderInbox)
	require.NoError(t, err)
	require.NoError(t, b.mb.Delete(FolderInbox, inbox[0].ID))
	require.Empty(t, send().Recipients[0].Err, "a delete makes room")
}

func TestExpiryRemovesOldMailFromEveryFolder(t *testing.T) {
	n := newTestNet()
	a, b := n.addVisor(t), n.addVisor(t)
	_, err := a.mb.Send(context.Background(), Outgoing{To: []string{addr("b", b.pk)}, Body: "x"})
	require.NoError(t, err)
	a.mb.SetLimits(Limits{MaxAge: time.Hour})

	removed, err := a.mb.Expire(time.Now())
	require.NoError(t, err)
	require.Zero(t, removed, "nothing is an hour old yet")
	removed, err = a.mb.Expire(time.Now().Add(2 * time.Hour))
	require.NoError(t, err)
	require.Equal(t, 1, removed, "the Sent copy expired")

	a.mb.SetLimits(Limits{MaxAge: -1})
	_, err = a.mb.Send(context.Background(), Outgoing{To: []string{addr("b", b.pk)}, Body: "x"})
	require.NoError(t, err)
	removed, err = a.mb.Expire(time.Now().Add(1000 * time.Hour))
	require.NoError(t, err)
	require.Zero(t, removed, "a negative MaxAge keeps everything")
}

func TestAttachmentsRoundTripByteForByte(t *testing.T) {
	n := newTestNet()
	a, b := n.addVisor(t), n.addVisor(t)
	bin := make([]byte, 3000)
	for i := range bin {
		bin[i] = byte(i)
	}
	res, err := a.mb.Send(context.Background(), Outgoing{
		To: []string{addr("b", b.pk)}, Subject: "files", Body: "see attached",
		Attachments: []OutgoingAttachment{
			{Name: "bytes.bin", Data: bin},
			{Name: "notes ü.txt", ContentType: "text/plain", Data: []byte("hello")},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "pipe", res.Recipients[0].Via, "the network of the connection that carried it (net.Pipe here)")

	inbox, err := b.mb.List(FolderInbox)
	require.NoError(t, err)
	raw, err := b.mb.Raw(FolderInbox, inbox[0].ID)
	require.NoError(t, err)
	r, err := Render(raw)
	require.NoError(t, err)
	require.Equal(t, "see attached\r\n", r.Text)
	require.Len(t, r.Attachments, 2)
	require.Equal(t, "notes ü.txt", r.Attachments[1].Name)

	got, err := b.mb.Attachment(FolderInbox, inbox[0].ID, 0)
	require.NoError(t, err)
	require.Equal(t, "bytes.bin", got.Name)
	require.Equal(t, "application/octet-stream", got.ContentType)
	require.Equal(t, bin, got.Data)
	_, err = b.mb.Attachment(FolderInbox, inbox[0].ID, 2)
	require.Error(t, err)
}

func TestSendToOwnPKReportsLocal(t *testing.T) {
	n := newTestNet()
	a := n.addVisor(t)
	res, err := a.mb.Send(context.Background(), Outgoing{To: []string{addr("me", a.pk)}, Body: "x"})
	require.NoError(t, err)
	require.Equal(t, "local", res.Recipients[0].Via)
}
