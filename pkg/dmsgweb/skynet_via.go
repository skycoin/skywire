// Package dmsgweb pkg/dmsgweb/skynet_via.go c1-net-dmsg
package dmsgweb

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skynetweb"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/transport/network/stcp"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

const (
	skynetViaRedial    = 10 * time.Second
	skynetViaHandshake = 8 * time.Second
)

// SkynetVia holds one stcp transport from this proxy's key to a visor and
// reaches other visors through it as a one-hop relay. The relay forwards a
// SYN signed by this key, so the destination sees this key and not the visor's.
type SkynetVia struct {
	visorPK cipher.PubKey
	tpM     *transport.Manager
	mux     *transport.VStreamMux
	log     *logging.Logger
}

// ParseSkynetVia splits "<pk>@<host:port>".
func ParseSkynetVia(s string) (cipher.PubKey, string, error) {
	var pk cipher.PubKey
	pkStr, addr, ok := strings.Cut(s, "@")
	if !ok || addr == "" {
		return pk, "", fmt.Errorf("skynet via %q: want <visor pk>@<host:port>", s)
	}
	if err := pk.Set(pkStr); err != nil {
		return pk, "", fmt.Errorf("skynet via %q: %w", s, err)
	}
	return pk, addr, nil
}

// StartSkynetVia opens the transport to visorPK at addr and keeps it open
// until ctx is done. Nothing is registered with transport discovery by this
// side, and the stcp listener it needs is bound to loopback.
func StartSkynetVia(ctx context.Context, log *logging.Logger, mLog *logging.MasterLogger, pk cipher.PubKey, sk cipher.SecKey, visorPK cipher.PubKey, addr string) (*SkynetVia, error) {
	eb := appevent.NewBroadcaster(log, 0)
	conf := transport.ManagerConfig{
		PubKey:           pk,
		SecKey:           sk,
		DiscoveryClient:  transport.NewDiscoveryMock(),
		LogStore:         transport.InMemoryTransportLogStore(),
		Version:          buildinfo.Version(),
		ARTransportLimit: -1,
	}
	factory := network.ClientFactory{
		PK:         pk,
		SK:         sk,
		ListenAddr: "127.0.0.1:0",
		PKTable:    stcp.NewTable(map[cipher.PubKey]string{visorPK: addr}),
		EB:         eb,
		MLogger:    mLog,
	}
	tpM, err := transport.NewManager(log, nil, eb, &conf, factory)
	if err != nil {
		return nil, err
	}
	tpM.InitClient(ctx, types.STCP, 0)
	mux := transport.NewVStreamMux(tpM, routing.SkynetForwardPacket, log)
	tpM.SetSkynetForwardHandler(mux.HandlePacket)
	tpM.Serve(ctx)

	s := &SkynetVia{visorPK: visorPK, tpM: tpM, mux: mux, log: log}
	go s.keepTransport(ctx)
	go func() {
		<-ctx.Done()
		_ = mux.Close() //nolint:errcheck
		tpM.Close()
	}()
	return s, nil
}

func (s *SkynetVia) keepTransport(ctx context.Context) {
	t := time.NewTicker(skynetViaRedial)
	defer t.Stop()
	for {
		if _, err := s.tpM.GetTransport(s.visorPK, types.STCP); err != nil {
			if _, err := s.tpM.SaveTransport(ctx, s.visorPK, types.STCP, transport.LabelUser); err != nil {
				s.log.WithError(err).WithField("visor", s.visorPK.String()).Warn("skynet via: transport to the visor failed")
			} else {
				s.log.WithField("visor", s.visorPK.String()).Info("skynet via: transport to the visor is up")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Dial reaches pk:port over skynet: directly when pk is the visor, else
// relayed through it. It fails when the visor has no non-dmsg transport to pk.
func (s *SkynetVia) Dial(ctx context.Context, pk cipher.PubKey, port uint16) (net.Conn, error) {
	var (
		st  *transport.VStream
		err error
	)
	if pk == s.visorPK {
		st, err = s.mux.Dial(pk, "")
	} else {
		st, err = s.mux.DialThroughRelay(s.visorPK, pk, "")
	}
	if err != nil {
		return nil, err
	}
	conn := &viaConn{VStream: st}
	done := make(chan error, 1)
	go func() { done <- skynetweb.PerformHandshake(conn, port) }()
	select {
	case err = <-done:
	case <-time.After(skynetViaHandshake):
		err = errors.New("skynet via: handshake timed out")
	case <-ctx.Done():
		err = ctx.Err()
	}
	if err != nil {
		_ = conn.Close() //nolint:errcheck
		return nil, err
	}
	return conn, nil
}

// viaConn gives a VStream the net.Conn methods it lacks.
type viaConn struct{ *transport.VStream }

func (c *viaConn) LocalAddr() net.Addr                { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }
func (c *viaConn) RemoteAddr() net.Addr               { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }
func (c *viaConn) SetDeadline(_ time.Time) error      { return nil }
func (c *viaConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *viaConn) SetWriteDeadline(_ time.Time) error { return nil }
