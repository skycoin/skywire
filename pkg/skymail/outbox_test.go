package skymail

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func (n *testNet) setDown(v *visor, down bool) {
	n.mu.Lock()
	n.down[v.pk] = down
	n.mu.Unlock()
}

func runOutbox(t *testing.T, v *visor) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { v.mb.RunOutbox(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

func folderLen(t *testing.T, v *visor, folder string) int {
	t.Helper()
	l, err := v.mb.List(folder)
	require.NoError(t, err)
	return len(l)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	require.Eventually(t, cond, 10*time.Second, 20*time.Millisecond, what)
}

func TestOfflineRecipientIsQueuedThenDelivered(t *testing.T) {
	n := newTestNet()
	a, b := n.addVisor(t), n.addVisor(t)
	runOutbox(t, a)
	n.setDown(b, true)

	start := time.Now()
	res, err := a.mb.Send(context.Background(), Outgoing{To: []string{addr("b", b.pk)}, Subject: "later", Body: "x"})
	require.NoError(t, err, "queued is accepted")
	require.Less(t, time.Since(start), 5*time.Second, "the sender is not held up")
	require.True(t, res.Recipients[0].Queued)
	require.Contains(t, res.Recipients[0].Err, "unreachable")
	require.NotEmpty(t, res.OutboxID)
	require.Empty(t, res.ID, "no Sent copy before anything is delivered")

	out, err := a.mb.List(FolderOutbox)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, res.OutboxID, out[0].ID)
	require.Equal(t, FolderOutbox, out[0].Folder)
	require.NotNil(t, out[0].Queue)
	require.Equal(t, []string{addr("b", b.pk)}, out[0].Queue.Pending)
	require.Equal(t, 1, out[0].Queue.Attempts)
	require.WithinDuration(t, time.Now().Add(time.Minute), out[0].Queue.NextAttempt, 5*time.Second)
	require.WithinDuration(t, time.Now().Add(DefaultMaxAge), out[0].Queue.GiveUp, 5*time.Second)
	require.Zero(t, folderLen(t, a, FolderSent))

	n.setDown(b, false)
	require.NoError(t, a.mb.Retry(""))
	waitFor(t, "delivered once B is back", func() bool { return folderLen(t, b, FolderInbox) == 1 })
	waitFor(t, "moved to Sent", func() bool { return folderLen(t, a, FolderSent) == 1 })
	require.Zero(t, folderLen(t, a, FolderOutbox))
	require.Zero(t, a.mb.Queued())
	inbox, _ := b.mb.List(FolderInbox) //nolint:errcheck
	require.Equal(t, "later", inbox[0].Subject)
	require.True(t, inbox[0].FromVerified, "the sender's own visor delivered it")
}

func TestMailFromTheRecipientWakesTheOutbox(t *testing.T) {
	n := newTestNet()
	a, b := n.addVisor(t), n.addVisor(t)
	runOutbox(t, a)
	n.setDown(b, true)
	_, err := a.mb.Send(context.Background(), Outgoing{To: []string{addr("b", b.pk)}, Body: "x"})
	require.NoError(t, err)
	n.setDown(b, false)

	// The next attempt is a minute away. B writing to A shows B is back.
	_, err = b.mb.Send(context.Background(), Outgoing{To: []string{addr("a", a.pk)}, Body: "hello"})
	require.NoError(t, err)
	waitFor(t, "delivered without waiting for the backoff", func() bool { return folderLen(t, b, FolderInbox) == 1 })
}

func TestOutboxSurvivesAReopen(t *testing.T) {
	n := newTestNet()
	a, b := n.addVisor(t), n.addVisor(t)
	n.setDown(b, true)
	res, err := a.mb.Send(context.Background(), Outgoing{To: []string{addr("b", b.pk)}, Body: "x"})
	require.NoError(t, err)

	re, err := Open(a.mb.cfg)
	require.NoError(t, err)
	require.Equal(t, 1, re.Queued())
	out, err := re.List(FolderOutbox)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, res.OutboxID, out[0].ID)
	require.Equal(t, []string{addr("b", b.pk)}, out[0].Queue.Pending)
	require.Contains(t, out[0].Queue.LastError, "unreachable")
	require.True(t, out[0].Queue.NextAttempt.IsZero(), "due at once after a restart")

	n.setDown(b, false)
	runOutbox(t, &visor{pk: a.pk, mb: re})
	waitFor(t, "the reopened outbox delivers", func() bool { return folderLen(t, b, FolderInbox) == 1 })
}

func TestDeletingFromTheOutboxCancelsDelivery(t *testing.T) {
	n := newTestNet()
	a, b := n.addVisor(t), n.addVisor(t)
	runOutbox(t, a)
	n.setDown(b, true)
	res, err := a.mb.Send(context.Background(), Outgoing{To: []string{addr("b", b.pk)}, Body: "x"})
	require.NoError(t, err)

	require.NoError(t, a.mb.Delete(FolderOutbox, res.OutboxID))
	require.Zero(t, a.mb.Queued())
	_, err = os.Stat(filepath.Join(a.mb.cfg.Dir, "."+FolderOutbox, queueDir, res.OutboxID+".json"))
	require.ErrorIs(t, err, os.ErrNotExist, "the record goes with the message")
	require.ErrorIs(t, a.mb.Retry(res.OutboxID), ErrNotFound)

	n.setDown(b, false)
	require.NoError(t, a.mb.Retry(""))
	time.Sleep(200 * time.Millisecond)
	require.Zero(t, folderLen(t, b, FolderInbox))
}

func TestPartialDeliveryKeepsOneSentCopy(t *testing.T) {
	n := newTestNet()
	a, b, c := n.addVisor(t), n.addVisor(t), n.addVisor(t)
	runOutbox(t, a)
	n.setDown(c, true)
	res, err := a.mb.Send(context.Background(), Outgoing{To: []string{addr("b", b.pk), addr("c", c.pk)}, Body: "x"})
	require.NoError(t, err)
	require.NotEmpty(t, res.ID)
	require.NotEmpty(t, res.OutboxID)
	require.Equal(t, 1, folderLen(t, a, FolderSent))

	n.setDown(c, false)
	require.NoError(t, a.mb.Retry(res.OutboxID))
	waitFor(t, "the rest is delivered", func() bool { return folderLen(t, c, FolderInbox) == 1 })
	waitFor(t, "the Outbox empties", func() bool { return a.mb.Queued() == 0 })
	require.Equal(t, 1, folderLen(t, a, FolderSent), "no second Sent copy")
	require.Equal(t, 1, folderLen(t, b, FolderInbox), "B is not sent it twice")
}

func TestOutboxGivesUpWithABounce(t *testing.T) {
	n := newTestNet()
	bounced := make(chan Delivered, 1)
	a := n.addVisor(t, func(c *Config) { c.OnDeliver = func(d Delivered) { bounced <- d } })
	b := n.addVisor(t)
	n.setDown(b, true)
	a.mb.SetLimits(Limits{MaxAge: 300 * time.Millisecond})
	runOutbox(t, a)

	res, err := a.mb.Send(context.Background(), Outgoing{To: []string{addr("b", b.pk)}, Subject: "lost", Body: "keep me"})
	require.NoError(t, err)
	orig, err := a.mb.Raw(FolderOutbox, res.OutboxID)
	require.NoError(t, err)

	var d Delivered
	select {
	case d = <-bounced:
	case <-time.After(10 * time.Second):
		t.Fatal("no bounce after MaxAge")
	}
	require.Equal(t, "Undelivered mail: lost", d.Subject)
	require.Zero(t, a.mb.Queued())
	require.Zero(t, folderLen(t, a, FolderOutbox))

	inbox, err := a.mb.List(FolderInbox)
	require.NoError(t, err)
	require.Len(t, inbox, 1)
	require.Equal(t, d.ID, inbox[0].ID)
	require.True(t, inbox[0].FromVerified, "this visor wrote it")
	r, err := a.mb.Raw(FolderInbox, d.ID)
	require.NoError(t, err)
	rendered, err := Render(r)
	require.NoError(t, err)
	require.Contains(t, rendered.Text, addr("b", b.pk))
	require.Contains(t, rendered.Text, "gave up after")
	require.Contains(t, rendered.Text, "unreachable")
	att, err := a.mb.Attachment(FolderInbox, d.ID, 0)
	require.NoError(t, err)
	require.Equal(t, "original.eml", att.Name)
	require.Equal(t, orig, att.Data, "the original comes back byte for byte")
}

func TestWithoutAnOutboxAnOfflineSendFailsNow(t *testing.T) {
	n := newTestNet()
	a := n.addVisor(t, func(c *Config) { c.Outbox = false })
	b := n.addVisor(t)
	n.setDown(b, true)
	start := time.Now()
	res, err := a.mb.Send(context.Background(), Outgoing{To: []string{addr("b", b.pk)}, Body: "x"})
	require.Error(t, err)
	require.Less(t, time.Since(start), 5*time.Second)
	require.False(t, res.Recipients[0].Queued)
	require.Empty(t, res.OutboxID)
	require.Zero(t, a.mb.Queued())
	require.ErrorIs(t, a.mb.Retry(""), ErrNoOutbox)
	_, err = a.mb.List(FolderOutbox)
	require.Error(t, err, "no Outbox folder")
	_, err = os.Stat(filepath.Join(a.mb.cfg.Dir, "."+FolderOutbox))
	require.ErrorIs(t, err, os.ErrNotExist, "nothing created on disk")
}
