// Package appevent pkg/app/appevent/handshake.go
package appevent

import (
	"fmt"
	"io"
	"net"
	"net/rpc"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/app/appcommon"
)

// DoReqHandshake performs a request handshake which is initiated from an app.
// First, it determines whether we need an egress connection (from the app server which sends events) by seeing if
// there are any subscriptions within 'subs'. If so, a listener is started.
// Then we send a hello object to the app server which contains the proc key and egress connection info (if needed).
func DoReqHandshake(conf appcommon.ProcConfig, subs *Subscriber) (net.Conn, []io.Closer, error) {
	var closers []io.Closer
	hello := appcommon.Hello{ProcKey: conf.ProcKey}

	// configure and serve event channel subscriptions (if any)
	if subs != nil && subs.Count() > 0 {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create listener for RPC egress: %w", err)
		}

		log := logrus.New().WithField("src", "events_gateway")

		rpcS := rpc.NewServer()
		if err := rpcS.RegisterName(conf.ProcKey.String(), NewRPCGateway(log, subs)); err != nil {
			panic(err) // should never happen
		}
		go rpcS.Accept(lis)

		hello.EgressNet = lis.Addr().Network()
		hello.EgressAddr = lis.Addr().String()
		hello.EventSubs = subs.Subscriptions()
		closers = append(closers, lis)
	}

	// dial to app server and send hello JSON object
	// sending hello will also advertise event subscriptions endpoint (if needed)
	conn, err := net.Dial("tcp", conf.AppSrvAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to dial to app server: %w", err)
	}
	if err := appcommon.WriteHello(conn, hello); err != nil {
		return nil, nil, fmt.Errorf("failed to send hello to app server: %w", err)
	}

	return conn, append(closers, conn), nil
}

// DoRespHandshake performs a response handshake from the app server side.
// It reads the hello object from the app, and connects the app to the events broadcast (if needed).
func DoRespHandshake(ebc *Broadcaster, conn net.Conn) (*appcommon.Hello, error) {
	hello, err := appcommon.ReadHello(conn)
	if err != nil {
		return nil, fmt.Errorf("failed to read hello object: %w", err)
	}

	// connect app to events broadcast (if necessary)
	if hello.EgressNet != "" && hello.EgressAddr != "" && len(hello.EventSubs) > 0 {
		rpcC, err := NewRPCClient(&hello)
		if err != nil {
			return nil, fmt.Errorf("failed to connect app to events broadcast: %w", err)
		}
		ebc.AddClient(rpcC)
	}

	return &hello, nil
}
