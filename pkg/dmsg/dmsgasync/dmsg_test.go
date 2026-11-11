// Package dmsgasync pkg/dmsg/dmsgasync/dmsg_test.go c1-net-dmsg
package dmsgasync

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
)

func dmsgDialer(c *dmsg.Client) Dialer {
	return func(ctx context.Context, pk cipher.PubKey, port uint16) (net.Conn, error) {
		return c.DialStream(ctx, dmsg.Addr{PK: pk, Port: port})
	}
}

// Over real dmsg: the recipient registers with a host, goes away, a sender
// deposits, and the recipient fetches the message with the sender's key as
// the stream authenticated it.
func TestOverDmsg(t *testing.T) {
	env := dmsgtest.NewEnv(t, 10*time.Second)
	require.NoError(t, env.Startup(0, 1, 0, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	hostC, err := env.NewClient(&dmsg.Config{MinSessions: 1})
	require.NoError(t, err)
	senderC, err := env.NewClient(&dmsg.Config{MinSessions: 1})
	require.NoError(t, err)
	rpk, rsk := cipher.GenerateKeyPair()
	rcptC, err := env.NewClientWithKeys(rpk, rsk, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)

	host, err := NewHost(HostConfig{
		PK:      hostC.LocalPK(),
		Dir:     t.TempDir(),
		Accepts: func(k cipher.PubKey) bool { return k == rpk },
	})
	require.NoError(t, err)
	l, err := hostC.Listen(Port)
	require.NoError(t, err)
	go host.Serve(ctx, l) //nolint:errcheck

	rec, err := NewRecord(rpk, rsk, []cipher.PubKey{hostC.LocalPK()}, 0, 1, time.Hour)
	require.NoError(t, err)
	require.NoError(t, Register(ctx, dmsgDialer(rcptC), hostC.LocalPK(), rec, Policy{Allow: []cipher.PubKey{senderC.LocalPK()}}))

	got, err := LookupRecord(ctx, dmsgDialer(senderC), hostC.LocalPK(), rpk)
	require.NoError(t, err)
	_, err = DepositAny(ctx, dmsgDialer(senderC), got, 25, NewRef(), []byte("while you were out"))
	require.NoError(t, err)

	var msgs []Message
	n, err := Fetch(ctx, dmsgDialer(rcptC), hostC.LocalPK(), []uint16{25}, func(m Message) error {
		msgs = append(msgs, m)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, senderC.LocalPK(), msgs[0].From)
	require.Equal(t, []byte("while you were out"), msgs[0].Payload)
	require.Zero(t, host.Stats().Messages)

	// The host's own key is not the recipient's, so another client cannot
	// deposit for a key the host does not hold.
	_, err = Deposit(ctx, dmsgDialer(senderC), hostC.LocalPK(), senderC.LocalPK(), 25, "", []byte("x"))
	require.ErrorIs(t, err, ErrNotHosted)
}
