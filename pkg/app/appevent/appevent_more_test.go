// Package appevent pkg/app/appevent/appevent_more_test.go: unit tests for the
// event value type, the typed event data, the Broadcaster send helpers, the
// Subscriber push/subscribe machinery, the RPC gateway/client, and the
// request/response handshakes.
package appevent

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appcommon"
)

// ---- event.go --------------------------------------------------------------

func TestEvent_NewUnmarshal(t *testing.T) {
	in := TCPDialData{RemoteNet: "tcp", RemoteAddr: "1.2.3.4:80"}
	e := NewEvent(TCPDial, in)
	require.Equal(t, TCPDial, e.Type)

	var out TCPDialData
	e.Unmarshal(&out)
	require.Equal(t, in, out)
}

func TestEvent_NewPanicsOnUnmarshalable(t *testing.T) {
	require.Panics(t, func() { NewEvent(TCPDial, make(chan int)) })
}

func TestEvent_UnmarshalPanicsOnBadData(t *testing.T) {
	e := &Event{Type: TCPDial, Data: []byte("not json")}
	require.Panics(t, func() {
		var out TCPDialData
		e.Unmarshal(&out)
	})
}

func TestEvent_DoneWait(t *testing.T) {
	e := NewEvent(TCPDial, TCPDialData{})
	e.InitDone()

	go e.Done()
	// Wait must return once Done closes the channel.
	done := make(chan struct{})
	go func() { e.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Wait did not return after Done")
	}
}

// ---- types.go --------------------------------------------------------------

func TestTypedData_Type(t *testing.T) {
	require.Equal(t, TCPDial, TCPDialData{}.Type())
	require.Equal(t, TCPClose, TCPCloseData{}.Type())
}

// ---- utils.go --------------------------------------------------------------

func TestBroadcaster_SendHelpers(t *testing.T) {
	eb := NewBroadcaster(nil, time.Second)
	defer func() { _ = eb.Close() }()

	// No clients registered → broadcasts are no-ops that must not panic.
	require.NotPanics(t, func() {
		eb.SendTCPDial(context.Background(), "tcp", "1.2.3.4:80")
		eb.SendTPClose(context.Background(), "tcp", "1.2.3.4:80")
	})
}

// ---- subscriber.go ---------------------------------------------------------

func TestSubscriber_SubscribeAndPush(t *testing.T) {
	s := NewSubscriber()
	require.Equal(t, 0, s.Count())

	dialCh := make(chan TCPDialData, 1)
	closeCh := make(chan TCPCloseData, 1)
	s.OnTCPDial(func(d TCPDialData) { dialCh <- d })
	s.OnTCPClose(func(d TCPCloseData) { closeCh <- d })

	require.Equal(t, 2, s.Count())
	subs := s.Subscriptions()
	require.True(t, subs[TCPDial])
	require.True(t, subs[TCPClose])

	// Push a subscribed dial event — PushEvent blocks until the handler
	// signals Done, so the value is available immediately after.
	require.NoError(t, PushEvent(s, NewEvent(TCPDial, TCPDialData{RemoteNet: "tcp", RemoteAddr: "a"})))
	require.Equal(t, "a", (<-dialCh).RemoteAddr)

	require.NoError(t, PushEvent(s, NewEvent(TCPClose, TCPCloseData{RemoteNet: "tcp", RemoteAddr: "b"})))
	require.Equal(t, "b", (<-closeCh).RemoteAddr)

	// An event with no matching subscription returns nil without blocking.
	require.NoError(t, PushEvent(s, NewEvent("unsubscribed_type", struct{}{})))
}

func TestSubscriber_Close(t *testing.T) {
	s := NewSubscriber()
	s.OnTCPDial(func(TCPDialData) {})

	require.NoError(t, s.Close())

	// Close nils out the subscription map (without flipping a closed flag),
	// so a subsequent push simply finds no channel for the type and is a
	// no-op returning nil rather than delivering anywhere.
	require.NoError(t, PushEvent(s, NewEvent(TCPDial, TCPDialData{})))
}

// ---- rpc.go ----------------------------------------------------------------

func TestNewRPCGateway_NilSubsPanics(t *testing.T) {
	require.Panics(t, func() { NewRPCGateway(nil, nil) })
}

func TestRPCGateway_Notify(t *testing.T) {
	s := NewSubscriber()
	gw := NewRPCGateway(nil, s) // nil log → default logger

	// No subscription for this type → PushEvent returns nil.
	require.NoError(t, gw.Notify(NewEvent(TCPDial, TCPDialData{}), nil))
}

func TestRPCClient_NoEgress(t *testing.T) {
	hello := &appcommon.Hello{ProcKey: appcommon.RandProcKey()}
	c, err := NewRPCClient(hello)
	require.NoError(t, err)

	// Nil underlying rpc client → Notify/Close are no-ops.
	require.NoError(t, c.Notify(context.Background(), NewEvent(TCPDial, TCPDialData{})))
	require.NoError(t, c.Close())
	require.Equal(t, hello, c.Hello())

	rc, ok := c.(*rpcClient)
	require.True(t, ok)
	require.Contains(t, rc.formatMethod("Notify"), "Notify")
	require.Contains(t, rc.formatMethod("Notify"), hello.ProcKey.String())
}

func TestNewRPCClient_DialError(t *testing.T) {
	hello := &appcommon.Hello{
		ProcKey:    appcommon.RandProcKey(),
		EgressNet:  "tcp",
		EgressAddr: "127.0.0.1:1", // refused
	}
	_, err := NewRPCClient(hello)
	require.Error(t, err)
}

// ---- handshake.go ----------------------------------------------------------

func TestHandshake_WithSubscriptions(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = lis.Close() }()

	ebc := NewBroadcaster(nil, time.Second)
	defer func() { _ = ebc.Close() }()

	helloCh := make(chan *appcommon.Hello, 1)
	errCh := make(chan error, 1)
	go func() {
		conn, aErr := lis.Accept()
		if aErr != nil {
			errCh <- aErr
			return
		}
		h, hErr := DoRespHandshake(ebc, conn)
		if hErr != nil {
			errCh <- hErr
			return
		}
		helloCh <- h
	}()

	subs := NewSubscriber()
	subs.OnTCPDial(func(TCPDialData) {}) // Count > 0 → egress listener path
	defer func() { _ = subs.Close() }()

	conf := appcommon.ProcConfig{ProcKey: appcommon.RandProcKey(), AppSrvAddr: lis.Addr().String()}
	conn, closers, err := DoReqHandshake(conf, subs)
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.NotEmpty(t, closers)
	defer func() {
		for _, c := range closers {
			_ = c.Close()
		}
	}()

	select {
	case h := <-helloCh:
		require.Equal(t, conf.ProcKey, h.ProcKey)
		require.NotEmpty(t, h.EventSubs)
		require.NotEmpty(t, h.EgressAddr)
	case err := <-errCh:
		t.Fatalf("response handshake failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("handshake timed out")
	}
}

func TestHandshake_NoSubscriptions(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = lis.Close() }()

	ebc := NewBroadcaster(nil, time.Second)
	defer func() { _ = ebc.Close() }()

	helloCh := make(chan *appcommon.Hello, 1)
	errCh := make(chan error, 1)
	go func() {
		conn, aErr := lis.Accept()
		if aErr != nil {
			errCh <- aErr
			return
		}
		h, hErr := DoRespHandshake(ebc, conn)
		if hErr != nil {
			errCh <- hErr
			return
		}
		helloCh <- h
	}()

	conf := appcommon.ProcConfig{ProcKey: appcommon.RandProcKey(), AppSrvAddr: lis.Addr().String()}
	conn, closers, err := DoReqHandshake(conf, nil) // nil subs → no egress
	require.NoError(t, err)
	require.NotNil(t, conn)
	defer func() {
		for _, c := range closers {
			_ = c.Close()
		}
	}()

	select {
	case h := <-helloCh:
		require.Equal(t, conf.ProcKey, h.ProcKey)
		require.Empty(t, h.EgressAddr) // no egress advertised
	case err := <-errCh:
		t.Fatalf("response handshake failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("handshake timed out")
	}
}

func TestDoReqHandshake_DialError(t *testing.T) {
	conf := appcommon.ProcConfig{ProcKey: appcommon.RandProcKey(), AppSrvAddr: "127.0.0.1:1"}
	_, _, err := DoReqHandshake(conf, nil)
	require.Error(t, err)
}

func TestDoRespHandshake_ReadError(t *testing.T) {
	a, b := net.Pipe()
	_ = a.Close() // peer closed → ReadHello fails
	defer func() { _ = b.Close() }()

	ebc := NewBroadcaster(nil, time.Second)
	_, err := DoRespHandshake(ebc, b)
	require.Error(t, err)
}
