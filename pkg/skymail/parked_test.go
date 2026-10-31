package skymail

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// fakeParkServer is a dmsg-async server reduced to what skymail sees.
type fakeParkServer struct {
	pk    cipher.PubKey
	mu    sync.Mutex
	held  map[cipher.PubKey][][]byte
	err   atomic.Pointer[error] // error to answer with, nil to store
	calls atomic.Int32
}

func newFakeParkServer() *fakeParkServer {
	pk, _ := cipher.GenerateKeyPair()
	return &fakeParkServer{pk: pk, held: map[cipher.PubKey][][]byte{}}
}

func (f *fakeParkServer) park(_ context.Context, dst cipher.PubKey, env []byte) ([]cipher.PubKey, error) {
	f.calls.Add(1)
	if e := f.err.Load(); e != nil {
		return nil, *e
	}
	f.mu.Lock()
	f.held[dst] = append(f.held[dst], env)
	f.mu.Unlock()
	return []cipher.PubKey{f.pk}, nil
}

func (f *fakeParkServer) taken(dst cipher.PubKey) [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.held[dst]
	delete(f.held, dst)
	return out
}

func TestParkedMailReachesTheRecipientWithEveryoneElseGone(t *testing.T) {
	n := newTestNet()
	srv := newFakeParkServer()
	a := n.addVisor(t, func(c *Config) { c.Park = srv.park })
	b := n.addVisor(t)
	n.setDown(b, true)
	stopA := startOutbox(t, a)

	_, err := a.mb.Send(context.Background(), Outgoing{From: "alice", To: []string{addr("bob", b.pk)}, Subject: "parked", Body: "hi"})
	require.NoError(t, err)
	waitFor(t, "parked", func() bool {
		q := queueOf(t, a)
		return len(q) == 1 && q[0].Queue.Parked == 1
	})

	// A goes away for good. B comes online and fetches.
	stopA()
	n.setDown(a, true)
	envs := srv.taken(b.pk)
	require.Len(t, envs, 1)
	require.True(t, IsSealed(envs[0]))
	require.NotContains(t, string(envs[0]), "parked", "the server cannot read it")

	require.NoError(t, b.mb.DeliverSealed(envs[0], srv.pk))
	inbox, err := b.mb.List(FolderInbox)
	require.NoError(t, err)
	require.Len(t, inbox, 1)
	require.Equal(t, "parked", inbox[0].Subject)
	require.Equal(t, a.pk.Hex(), inbox[0].PeerPK)
	require.True(t, inbox[0].FromVerified)
	require.Equal(t, srv.pk.Hex(), inbox[0].RelayPK, "the server that held it")

	require.NoError(t, b.mb.DeliverSealed(envs[0], srv.pk), "a second copy is answered as delivered")
	require.Equal(t, 1, folderLen(t, b, FolderInbox))
}

func TestMailTooBigToParkIsNotOfferedAgain(t *testing.T) {
	n := newTestNet()
	srv := newFakeParkServer()
	tooBig := ErrTooBigToPark
	srv.err.Store(&tooBig)
	a := n.addVisor(t, func(c *Config) { c.Park = srv.park })
	b := n.addVisor(t)
	n.setDown(b, true)
	startOutbox(t, a)
	_, err := a.mb.Send(context.Background(), Outgoing{To: []string{addr("bob", b.pk)}, Body: "x"})
	require.NoError(t, err)
	waitFor(t, "offered once", func() bool { return srv.calls.Load() == 1 })
	require.NoError(t, a.mb.Retry(""))
	time.Sleep(300 * time.Millisecond)
	require.Equal(t, int32(1), srv.calls.Load(), "size does not change, so it is not asked again")
	require.Zero(t, queueOf(t, a)[0].Queue.Parked)
}

func TestParkingWaitsWhenNoServerHoldsMessages(t *testing.T) {
	n := newTestNet()
	srv := newFakeParkServer()
	nowhere := ErrNowhereToPark
	srv.err.Store(&nowhere)
	a := n.addVisor(t, func(c *Config) { c.Park = srv.park })
	b := n.addVisor(t)
	n.setDown(b, true)
	startOutbox(t, a)
	_, err := a.mb.Send(context.Background(), Outgoing{To: []string{addr("bob", b.pk)}, Body: "x"})
	require.NoError(t, err)
	waitFor(t, "offered", func() bool { return srv.calls.Load() >= 1 })
	time.Sleep(200 * time.Millisecond)
	require.Equal(t, int32(1), srv.calls.Load(), "it backs off rather than asking in a loop")

	srv.err.Store(nil)
	require.NoError(t, a.mb.Retry(""))
	waitFor(t, "parked once a server holds messages", func() bool {
		q := queueOf(t, a)
		return len(q) == 1 && q[0].Queue.Parked == 1
	})
}

func TestDeliverSealedDropsWhatCanNeverBeFiledAndKeepsWhatCanLater(t *testing.T) {
	n := newTestNet()
	a, b, c := n.addVisor(t), n.addVisor(t), n.addVisor(t)
	via, _ := cipher.GenerateKeyPair()
	msg := []byte("Message-ID: <p@x>\r\nSubject: s\r\n\r\nx\r\n")

	require.NoError(t, b.mb.DeliverSealed([]byte("Subject: plain\r\n\r\nx\r\n"), via), "not sealed: dropped")
	toC, err := Seal(msg, a.sk, c.pk)
	require.NoError(t, err)
	require.NoError(t, b.mb.DeliverSealed(toC, via), "sealed for another visor: dropped")
	require.Zero(t, folderLen(t, b, FolderInbox))

	require.NoError(t, b.mb.SetWhitelist([]cipher.PubKey{c.pk}))
	env, err := Seal(msg, a.sk, b.pk)
	require.NoError(t, err)
	require.NoError(t, b.mb.DeliverSealed(env, via), "a sender this mailbox refuses: dropped")
	require.Zero(t, folderLen(t, b, FolderInbox))
	require.NoError(t, b.mb.SetWhitelist(nil))

	used, err := b.mb.Usage()
	require.NoError(t, err)
	b.mb.SetLimits(Limits{MaxTotalSize: used + 10})
	require.Error(t, b.mb.DeliverSealed(env, via), "a full mailbox keeps it on the server for later")
	b.mb.SetLimits(Limits{})
	require.NoError(t, b.mb.DeliverSealed(env, via))
	require.Equal(t, 1, folderLen(t, b, FolderInbox), "the earlier failure did not mark it seen")

	noKey := n.addVisor(t, func(c *Config) { c.SK = cipher.SecKey{} })
	require.Error(t, noKey.mb.DeliverSealed(env, via))
}

func TestCarriedMailIsNotParkedAndNothingParksWithoutAKey(t *testing.T) {
	hn := newHelperNet(t)
	srv := newFakeParkServer()
	hn.h.mb.cfg.Park = srv.park
	startOutbox(t, hn.a)
	startOutbox(t, hn.h)
	_, err := hn.a.mb.Send(context.Background(), Outgoing{To: []string{addr("bob", hn.b.pk)}, Body: "x"})
	require.NoError(t, err)
	waitFor(t, "carried", func() bool { return hn.h.mb.Queued() == 1 })
	time.Sleep(200 * time.Millisecond)
	require.Zero(t, srv.calls.Load(), "a helper does not park mail it carries for another visor")

	n := newTestNet()
	noKey := n.addVisor(t, func(c *Config) { c.SK = cipher.SecKey{}; c.Park = srv.park })
	off := n.addVisor(t)
	n.setDown(off, true)
	startOutbox(t, noKey)
	_, err = noKey.mb.Send(context.Background(), Outgoing{To: []string{addr("bob", off.pk)}, Body: "x"})
	require.NoError(t, err)
	time.Sleep(200 * time.Millisecond)
	require.Zero(t, srv.calls.Load(), "parking seals, so it needs the key")
}
