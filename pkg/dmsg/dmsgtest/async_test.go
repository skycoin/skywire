package dmsgtest

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

func asyncEnv(t *testing.T, servers int, store dmsg.AsyncStoreConfig) *Env {
	t.Helper()
	env := NewEnv(t, 30*time.Second)
	require.NoError(t, env.Startup(0, 0, 0, nil))
	t.Cleanup(env.Shutdown)
	for i := 0; i < servers; i++ {
		s := store
		_, err := env.NewServerWithConfig(dmsg.ServerConfig{Async: &s})
		require.NoError(t, err)
	}
	return env
}

func fetchingClient(t *testing.T, env *Env, pk cipher.PubKey, sk cipher.SecKey) *dmsg.Client {
	t.Helper()
	c, err := env.NewClientWithKeys(pk, sk, &dmsg.Config{
		MinSessions: 1,
		Async:       &dmsg.AsyncClientConfig{FetchInterval: time.Hour},
	})
	require.NoError(t, err)
	return c
}

func serverHolding(env *Env) (msgs int) {
	for _, s := range env.AllServers() {
		n, _, _, _ := s.AsyncStats()
		msgs += n
	}
	return msgs
}

func TestAsyncServerAdvertisesItsLimits(t *testing.T) {
	env := asyncEnv(t, 1, dmsg.AsyncStoreConfig{MaxMessage: 1000, TTL: time.Hour})
	plain, err := env.NewServer(dmsg.DefaultUpdateInterval)
	require.NoError(t, err)

	ctx := context.Background()
	for _, s := range env.AllServers() {
		e, err := env.Discovery().Entry(ctx, s.LocalPK())
		require.NoError(t, err)
		if s.LocalPK() == plain.LocalPK() {
			require.Nil(t, e.Server.Async, "a server without it advertises nothing")
			_, _, _, ok := s.AsyncStats()
			require.False(t, ok)
			continue
		}
		require.NotNil(t, e.Server.Async)
		require.Equal(t, 1000, e.Server.Async.MaxMessage)
		require.Equal(t, int64(3600), e.Server.Async.TTL)
	}
}

func TestAsyncMessageWaitsForAnOfflineRecipient(t *testing.T) {
	env := asyncEnv(t, 1, dmsg.AsyncStoreConfig{})
	sender, err := env.NewClient(&dmsg.Config{MinSessions: 1})
	require.NoError(t, err)
	rpk, rsk := cipher.GenerateKeyPair()

	ctx := context.Background()
	stored, err := sender.Deposit(ctx, rpk, 25, []byte("left while you were away"))
	require.NoError(t, err)
	require.Len(t, stored, 1)
	require.Equal(t, 1, serverHolding(env))

	got := make(chan dmsg.AsyncMessage, 4)
	r := fetchingClient(t, env, rpk, rsk)
	require.NoError(t, r.HandleAsync(25, func(m dmsg.AsyncMessage) error { got <- m; return nil }))
	select {
	case m := <-got:
		require.Equal(t, "left while you were away", string(m.Payload))
		require.Equal(t, sender.LocalPK(), m.From, "the server took the sender from its session")
		require.Equal(t, uint16(25), m.Port)
		require.Equal(t, stored[0], m.Server)
	case <-time.After(20 * time.Second):
		t.Fatal("the recipient did not fetch")
	}
	require.Eventually(t, func() bool { return serverHolding(env) == 0 }, 5*time.Second, 50*time.Millisecond,
		"the ack deletes it")
}

func TestAsyncHandlerErrorKeepsTheMessage(t *testing.T) {
	env := asyncEnv(t, 1, dmsg.AsyncStoreConfig{})
	sender, err := env.NewClient(&dmsg.Config{MinSessions: 1})
	require.NoError(t, err)
	rpk, rsk := cipher.GenerateKeyPair()
	ctx := context.Background()
	_, err = sender.Deposit(ctx, rpk, 25, []byte("x"))
	require.NoError(t, err)

	r := fetchingClient(t, env, rpk, rsk)
	var (
		calls atomic.Int32
		allow atomic.Bool
	)
	require.NoError(t, r.HandleAsync(25, func(dmsg.AsyncMessage) error {
		calls.Add(1)
		if !allow.Load() {
			return errors.New("not now")
		}
		return nil
	}))
	require.Eventually(t, func() bool { return calls.Load() >= 1 }, 20*time.Second, 50*time.Millisecond)
	n, err := r.FetchAsync(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
	require.Equal(t, 1, serverHolding(env), "a refused message stays")

	allow.Store(true)
	n, err = r.FetchAsync(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, 0, serverHolding(env))
}

func TestAsyncFetchesOnlyPortsWithAHandler(t *testing.T) {
	env := asyncEnv(t, 1, dmsg.AsyncStoreConfig{})
	sender, err := env.NewClient(&dmsg.Config{MinSessions: 1})
	require.NoError(t, err)
	rpk, rsk := cipher.GenerateKeyPair()
	ctx := context.Background()
	_, err = sender.Deposit(ctx, rpk, 25, []byte("mail"))
	require.NoError(t, err)
	_, err = sender.Deposit(ctx, rpk, 26, []byte("for an app not running"))
	require.NoError(t, err)

	r := fetchingClient(t, env, rpk, rsk)
	n, err := r.FetchAsync(ctx)
	require.NoError(t, err)
	require.Zero(t, n, "no handler, nothing fetched")

	got := make(chan string, 4)
	require.NoError(t, r.HandleAsync(25, func(m dmsg.AsyncMessage) error { got <- string(m.Payload); return nil }))
	_, err = r.FetchAsync(ctx)
	require.NoError(t, err)
	require.Equal(t, "mail", <-got)
	require.Equal(t, 1, serverHolding(env), "port 26 still waits")

	require.ErrorIs(t, r.HandleAsync(0, func(dmsg.AsyncMessage) error { return nil }), dmsg.ErrAsyncBadRequest)
	require.NoError(t, r.HandleAsync(25, nil), "nil removes the handler")
}

func TestAsyncSizeLimit(t *testing.T) {
	env := asyncEnv(t, 1, dmsg.AsyncStoreConfig{MaxMessage: 100})
	sender, err := env.NewClient(&dmsg.Config{MinSessions: 1})
	require.NoError(t, err)
	rpk, _ := cipher.GenerateKeyPair()
	ctx := context.Background()
	_, err = sender.Deposit(ctx, rpk, 25, make([]byte, 101))
	require.ErrorIs(t, err, dmsg.ErrAsyncTooBig)
	_, err = sender.Deposit(ctx, rpk, 25, make([]byte, 100))
	require.NoError(t, err)
	_, err = sender.Deposit(ctx, cipher.PubKey{}, 25, []byte("x"))
	require.ErrorIs(t, err, dmsg.ErrAsyncBadRequest)
}

func TestAsyncRecipientQuotaIsReported(t *testing.T) {
	env := asyncEnv(t, 1, dmsg.AsyncStoreConfig{MaxPerRecipient: 1})
	sender, err := env.NewClient(&dmsg.Config{MinSessions: 1})
	require.NoError(t, err)
	rpk, _ := cipher.GenerateKeyPair()
	ctx := context.Background()
	_, err = sender.Deposit(ctx, rpk, 25, []byte("one"))
	require.NoError(t, err)
	_, err = sender.Deposit(ctx, rpk, 25, []byte("two"))
	require.ErrorIs(t, err, dmsg.ErrAsyncRecipientFull, "the server's reason reaches the sender")
}

func TestAsyncGoesToTheSameServersEveryoneComputes(t *testing.T) {
	env := asyncEnv(t, 3, dmsg.AsyncStoreConfig{})
	sender, err := env.NewClient(&dmsg.Config{MinSessions: 1})
	require.NoError(t, err)
	rpk, rsk := cipher.GenerateKeyPair()
	ctx := context.Background()

	stored, err := sender.Deposit(ctx, rpk, 25, []byte("twice"))
	require.NoError(t, err)
	require.Len(t, stored, dmsg.AsyncDefaultReplicas)
	entries, err := env.Discovery().AllServers(ctx)
	require.NoError(t, err)
	want := dmsg.AsyncServersFor(entries, rpk, dmsg.AsyncDefaultReplicas)
	require.ElementsMatch(t, []cipher.PubKey{want[0].Static, want[1].Static}, stored)

	got := make(chan cipher.PubKey, 4)
	r := fetchingClient(t, env, rpk, rsk)
	require.NoError(t, r.HandleAsync(25, func(m dmsg.AsyncMessage) error { got <- m.Server; return nil }))
	_, err = r.FetchAsync(ctx)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return len(got) == 2 }, 20*time.Second, 50*time.Millisecond,
		"the recipient reads both copies; dropping the repeat is the handler's job")
	require.Equal(t, 0, serverHolding(env))
}

func TestAsyncWithoutAnyAsyncServer(t *testing.T) {
	env := NewEnv(t, 30*time.Second)
	require.NoError(t, env.Startup(0, 1, 1, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)
	c := env.AllClients()[0]
	rpk, _ := cipher.GenerateKeyPair()
	_, err := c.Deposit(context.Background(), rpk, 25, []byte("x"))
	require.ErrorIs(t, err, dmsg.ErrAsyncNoServers)
	require.ErrorIs(t, c.HandleAsync(25, func(dmsg.AsyncMessage) error { return nil }), dmsg.ErrAsyncDisabled,
		"fetching needs Config.Async")
	_, err = c.FetchAsync(context.Background())
	require.ErrorIs(t, err, dmsg.ErrAsyncDisabled)
}
