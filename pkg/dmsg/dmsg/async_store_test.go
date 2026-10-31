package dmsg

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

func newTestStore(t *testing.T, cfg AsyncStoreConfig) *asyncStore {
	t.Helper()
	st, err := newAsyncStore(cfg, logrus.New())
	require.NoError(t, err)
	return st
}

func asyncPK(t *testing.T) cipher.PubKey {
	t.Helper()
	p, _ := cipher.GenerateKeyPair()
	return p
}

func TestAsyncStoreDepositFetchAck(t *testing.T) {
	st := newTestStore(t, AsyncStoreConfig{})
	a, b := asyncPK(t), asyncPK(t)
	now := time.Now()
	id1, err := st.deposit(a, b, 25, []byte("one"), now)
	require.NoError(t, err)
	id2, err := st.deposit(a, b, 25, []byte("two"), now.Add(time.Millisecond))
	require.NoError(t, err)

	msgs, more := st.fetch(b, []uint16{25})
	require.False(t, more)
	require.Len(t, msgs, 2)
	require.Equal(t, "one", string(msgs[0].Payload), "oldest first")
	require.Equal(t, a, msgs[0].From)
	require.Equal(t, uint16(25), msgs[0].Port)

	again, _ := st.fetch(b, []uint16{25})
	require.Len(t, again, 2, "nothing goes until it is acked")
	require.Equal(t, 1, st.ack(b, []string{id1}))
	require.Equal(t, 0, st.ack(a, []string{id2}), "only the recipient can ack")
	left, _ := st.fetch(b, []uint16{25})
	require.Len(t, left, 1)
	require.Equal(t, id2, left[0].ID)
	require.Equal(t, 0, st.ack(b, []string{id1, "nope"}), "acking twice deletes nothing")

	n, bytes, r := st.stats()
	require.Equal(t, 1, n)
	require.Equal(t, int64(3), bytes)
	require.Equal(t, 1, r)
}

func TestAsyncStoreFetchOnlyThePortsAsked(t *testing.T) {
	st := newTestStore(t, AsyncStoreConfig{})
	a, b := asyncPK(t), asyncPK(t)
	for i := 0; i < asyncFetchBatchCount+5; i++ {
		_, err := st.deposit(a, b, 99, []byte("nobody serves this port"), time.Now())
		require.NoError(t, err)
	}
	_, err := st.deposit(a, b, 25, []byte("mail"), time.Now())
	require.NoError(t, err)
	msgs, more := st.fetch(b, []uint16{25})
	require.False(t, more)
	require.Len(t, msgs, 1, "older messages for an unserved port do not stand in front")
	require.Equal(t, "mail", string(msgs[0].Payload))
	none, _ := st.fetch(b, nil)
	require.Empty(t, none)
}

func TestAsyncStoreLimits(t *testing.T) {
	a, b, c := asyncPK(t), asyncPK(t), asyncPK(t)
	now := time.Now()

	st := newTestStore(t, AsyncStoreConfig{MaxMessage: 10})
	_, err := st.deposit(a, b, 1, make([]byte, 11), now)
	require.ErrorIs(t, err, ErrAsyncTooBig)
	_, err = st.deposit(a, cipher.PubKey{}, 1, nil, now)
	require.ErrorIs(t, err, ErrAsyncBadRequest)
	_, err = st.deposit(a, b, 0, nil, now)
	require.ErrorIs(t, err, ErrAsyncBadRequest)

	st = newTestStore(t, AsyncStoreConfig{MaxPerRecipient: 2})
	for i := 0; i < 2; i++ {
		_, err = st.deposit(a, b, 1, []byte("x"), now)
		require.NoError(t, err)
	}
	_, err = st.deposit(c, b, 1, []byte("x"), now)
	require.ErrorIs(t, err, ErrAsyncRecipientFull)
	_, err = st.deposit(a, c, 1, []byte("x"), now)
	require.NoError(t, err, "another recipient is not affected")

	st = newTestStore(t, AsyncStoreConfig{MaxRecipientBytes: 5})
	_, err = st.deposit(a, b, 1, []byte("abc"), now)
	require.NoError(t, err)
	_, err = st.deposit(a, b, 1, []byte("abc"), now)
	require.ErrorIs(t, err, ErrAsyncRecipientFull)

	st = newTestStore(t, AsyncStoreConfig{MaxSenderPerHour: 2})
	for i := 0; i < 2; i++ {
		_, err = st.deposit(a, b, 1, []byte("x"), now)
		require.NoError(t, err)
	}
	_, err = st.deposit(a, c, 1, []byte("x"), now)
	require.ErrorIs(t, err, ErrAsyncSenderLimit, "per sender, whoever the recipient")
	_, err = st.deposit(c, b, 1, []byte("x"), now)
	require.NoError(t, err, "another sender is not affected")
	_, err = st.deposit(a, c, 1, []byte("x"), now.Add(time.Hour+time.Second))
	require.NoError(t, err, "the window slides")

	st = newTestStore(t, AsyncStoreConfig{MaxTotal: 5})
	_, err = st.deposit(a, b, 1, []byte("abc"), now)
	require.NoError(t, err)
	_, err = st.deposit(a, c, 1, []byte("abc"), now)
	require.ErrorIs(t, err, ErrAsyncStoreFull)

	st = newTestStore(t, AsyncStoreConfig{MaxPerRecipient: -1, MaxRecipientBytes: -1, MaxSenderPerHour: -1, MaxTotal: -1})
	for i := 0; i < AsyncDefaultPerRecipient+10; i++ {
		_, err = st.deposit(a, b, 1, []byte("x"), now)
		require.NoError(t, err, "a negative bound is no bound")
	}

	st = newTestStore(t, AsyncStoreConfig{MaxMessage: -1})
	require.Equal(t, asyncFetchBatchBytes, st.cfg.MaxMessage, "message size is capped even when unbounded")
	_, err = newAsyncStore(AsyncStoreConfig{MaxMessage: asyncFetchBatchBytes + 1}, logrus.New())
	require.Error(t, err)
}

func TestAsyncStoreBatches(t *testing.T) {
	st := newTestStore(t, AsyncStoreConfig{MaxPerRecipient: -1, MaxSenderPerHour: -1, MaxRecipientBytes: -1})
	a, b := asyncPK(t), asyncPK(t)
	for i := 0; i < asyncFetchBatchCount+1; i++ {
		_, err := st.deposit(a, b, 1, []byte("x"), time.Now())
		require.NoError(t, err)
	}
	msgs, more := st.fetch(b, []uint16{1})
	require.Len(t, msgs, asyncFetchBatchCount)
	require.True(t, more)

	st = newTestStore(t, AsyncStoreConfig{MaxMessage: -1, MaxRecipientBytes: -1})
	big := make([]byte, asyncFetchBatchBytes)
	for i := 0; i < 2; i++ {
		_, err := st.deposit(a, b, 1, big, time.Now())
		require.NoError(t, err)
	}
	msgs, more = st.fetch(b, []uint16{1})
	require.Len(t, msgs, 1, "a full-size message fills a batch")
	require.True(t, more)
}

func TestAsyncStoreExpiry(t *testing.T) {
	st := newTestStore(t, AsyncStoreConfig{TTL: time.Hour})
	a, b := asyncPK(t), asyncPK(t)
	now := time.Now()
	_, err := st.deposit(a, b, 1, []byte("old"), now.Add(-2*time.Hour))
	require.NoError(t, err)
	_, err = st.deposit(a, b, 1, []byte("new"), now)
	require.NoError(t, err)
	require.Equal(t, 1, st.sweep(now))
	msgs, _ := st.fetch(b, []uint16{1})
	require.Len(t, msgs, 1)
	require.Equal(t, "new", string(msgs[0].Payload))

	keep := newTestStore(t, AsyncStoreConfig{TTL: -1})
	_, err = keep.deposit(a, b, 1, []byte("x"), now.Add(-1000*time.Hour))
	require.NoError(t, err)
	require.Zero(t, keep.sweep(now), "a negative TTL keeps messages")
	require.Zero(t, keep.cfg.info().TTL)
}

func TestAsyncStoreSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	cfg := AsyncStoreConfig{Dir: dir, TTL: time.Hour}
	st := newTestStore(t, cfg)
	a, b := asyncPK(t), asyncPK(t)
	now := time.Now()
	id, err := st.deposit(a, b, 7, []byte("kept"), now)
	require.NoError(t, err)
	gone, err := st.deposit(a, b, 7, []byte("acked"), now)
	require.NoError(t, err)
	require.Equal(t, 1, st.ack(b, []string{gone}))
	_, err = st.deposit(a, b, 7, []byte("expires"), now.Add(-2*time.Hour))
	require.NoError(t, err)

	// What a crash or a stranger can leave behind.
	rdir := filepath.Join(dir, b.Hex())
	require.NoError(t, os.WriteFile(filepath.Join(rdir, "123.ab"), []byte("not a message"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(rdir, "1.aa.tmp"), []byte("half"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(rdir, "../../escape"), []byte("x"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "not-a-pk"), 0o700))

	re := newTestStore(t, cfg)
	msgs, _ := re.fetch(b, []uint16{7})
	require.Len(t, msgs, 1, "only the live message comes back")
	require.Equal(t, id, msgs[0].ID)
	require.Equal(t, "kept", string(msgs[0].Payload))
	require.Equal(t, a, msgs[0].From)
	n, size, _ := re.stats()
	require.Equal(t, 1, n)
	require.Equal(t, int64(4), size, "quota accounting is rebuilt")
	files, err := os.ReadDir(rdir)
	require.NoError(t, err)
	require.Len(t, files, 1, "the expired, corrupt and half-written files are cleared")

	require.Equal(t, 1, re.ack(b, []string{id}))
	_, err = os.Stat(filepath.Join(rdir, id))
	require.ErrorIs(t, err, os.ErrNotExist, "an ack removes the file")
}

func TestAsyncFrames(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, writeAsyncFrame(&buf, []byte("hello")))
	got, err := readAsyncFrame(&buf, 5)
	require.NoError(t, err)
	require.Equal(t, "hello", string(got))

	buf.Reset()
	require.NoError(t, writeAsyncFrame(&buf, make([]byte, 6)))
	_, err = readAsyncFrame(&buf, 5)
	require.ErrorIs(t, err, ErrAsyncBadRequest, "a frame over the limit is refused before it is read")

	buf.Reset()
	require.ErrorIs(t, writeAsyncFrame(&buf, make([]byte, asyncMaxFrame+1)), ErrAsyncTooBig)
	require.Zero(t, buf.Len(), "nothing is written for a frame no reader takes")

	buf.Reset()
	require.NoError(t, writeAsyncFrame(&buf, []byte("{not json")))
	var op asyncOp
	require.ErrorIs(t, readAsyncJSON(&buf, &op), ErrAsyncBadRequest)

	require.ErrorIs(t, asyncReply{Code: asyncCodeRecipientFull}.err(), ErrAsyncRecipientFull)
	require.ErrorIs(t, asyncReply{Code: "from the future"}.err(), ErrAsyncInternal)
	require.NoError(t, asyncReply{OK: true}.err())
	require.Equal(t, asyncCodeSenderLimit, asyncErrCode(ErrAsyncSenderLimit))
}

func asyncEntry(t *testing.T) *disc.Entry {
	t.Helper()
	e := disc.NewServerEntry(asyncPK(t), 0, "127.0.0.1:1", 10)
	e.Server.Async = &disc.AsyncInfo{MaxMessage: 1024, TTL: 60}
	return e
}

func TestAsyncServersForIsStableAndSkipsServersWithoutIt(t *testing.T) {
	var entries []*disc.Entry
	for i := 0; i < 6; i++ {
		entries = append(entries, asyncEntry(t))
	}
	plain := disc.NewServerEntry(asyncPK(t), 0, "127.0.0.1:2", 10)
	dst := asyncPK(t)

	got := AsyncServersFor(append(append([]*disc.Entry{}, entries...), plain), dst, 2)
	require.Len(t, got, 2)
	for _, e := range got {
		require.NotEqual(t, plain.Static, e.Static, "a server without dmsg-async is never picked")
	}

	// Order of the list does not matter.
	rev := make([]*disc.Entry, len(entries))
	for i, e := range entries {
		rev[len(entries)-1-i] = e
	}
	again := AsyncServersFor(rev, dst, 2)
	require.Equal(t, got[0].Static, again[0].Static)
	require.Equal(t, got[1].Static, again[1].Static)

	// A server that is neither pick can leave without moving the picks.
	var others []*disc.Entry
	for _, e := range entries {
		if e.Static != got[0].Static && e.Static != got[1].Static {
			others = append(others, e)
		}
	}
	without := AsyncServersFor(append([]*disc.Entry{got[0], got[1]}, others[1:]...), dst, 2)
	require.Equal(t, got[0].Static, without[0].Static)
	require.Equal(t, got[1].Static, without[1].Static)

	require.Len(t, AsyncServersFor(entries[:1], dst, 3), 1, "k larger than the list")
	require.Empty(t, AsyncServersFor([]*disc.Entry{plain, nil}, dst, 2))
	require.Len(t, AsyncServersFor([]*disc.Entry{entries[0], entries[0]}, dst, 2), 1, "a duplicate counts once")
}
