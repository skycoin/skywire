package dht

import (
	"context"
	"fmt"
	"io"

	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
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
func (t *DMSGTransport) Dial(ctx context.Context, pk cipher.PubKey) (io.ReadWriteCloser, error) {
	stream, err := t.client.DialStream(ctx, dmsg.Addr{PK: pk, Port: t.port})
	if err != nil {
		return nil, fmt.Errorf("dht dmsg dial %s: %w", pk.String()[:8], err)
	}
	return stream, nil
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
