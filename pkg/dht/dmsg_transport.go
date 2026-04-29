package dht

import (
	"context"
	"fmt"
	"io"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// DMSGTransport implements Transport over DMSG streams.
type DMSGTransport struct {
	client *dmsg.Client
	port   uint16
}

// NewDMSGTransport creates a transport that dials and listens on the
// given DMSG client at the DHT port.
func NewDMSGTransport(client *dmsg.Client) *DMSGTransport {
	return &DMSGTransport{
		client: client,
		port:   skyenv.DmsgDHTPort,
	}
}

// Dial opens a DMSG stream to the remote peer's DHT port.
// If the standard DialStream fails (e.g., the target is a DMSG server
// with a server-type discovery entry), it falls back to dialing through
// an existing session — this handles DMSG servers that run DHT nodes
// via the direct package, which don't have client discovery entries.
func (t *DMSGTransport) Dial(ctx context.Context, pk cipher.PubKey) (io.ReadWriteCloser, error) {
	addr := dmsg.Addr{PK: pk, Port: t.port}
	stream, err := t.client.DialStream(ctx, addr)
	if err == nil {
		return stream, nil
	}

	// DialStream failed — try dialing through existing sessions.
	// If we already have a session to this PK (it's a DMSG server we're
	// connected to), open a stream directly on that session.
	if ses, ok := t.client.Session(pk); ok {
		directStream, dErr := ses.DialStream(ctx, addr)
		if dErr == nil {
			return directStream, nil
		}
	}

	return nil, fmt.Errorf("dht dmsg dial %s: %w", pk.String(), err)
}

// Listen starts accepting incoming DMSG streams on the DHT port.
func (t *DMSGTransport) Listen() (Listener, error) {
	lis, err := t.client.Listen(t.port)
	if err != nil {
		return nil, fmt.Errorf("dht dmsg listen port %d: %w", t.port, err)
	}
	return &dmsgListener{lis: lis}, nil
}

type dmsgListener struct {
	lis *dmsg.Listener
}

func (l *dmsgListener) Accept() (io.ReadWriteCloser, cipher.PubKey, error) {
	stream, err := l.lis.AcceptStream()
	if err != nil {
		return nil, cipher.PubKey{}, err
	}
	remotePK := stream.RawRemoteAddr().PK
	return stream, remotePK, nil
}

func (l *dmsgListener) Close() error {
	return l.lis.Close()
}
