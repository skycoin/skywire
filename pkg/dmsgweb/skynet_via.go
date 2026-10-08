// Package dmsgweb pkg/dmsgweb/skynet_via.go c1-net-dmsg
package dmsgweb

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/direct"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skynetweb"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	"github.com/skycoin/skywire/pkg/transport/network/stcp"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

const (
	skynetViaRedial    = 10 * time.Second
	skynetViaHandshake = 8 * time.Second
)

// SkynetViaTypes are the transport types that can dial a visor at a fixed
// address. sudph and webrtc need the address resolver or signaling to connect.
var SkynetViaTypes = []types.Type{types.STCPR, types.WS, types.QUIC, types.STCP}

// SkynetVia holds one transport from this proxy's key to a visor and reaches
// other visors through it as a one-hop relay. The relay forwards a SYN signed
// by this key, so the destination sees this key and not the visor's.
type SkynetVia struct {
	visorPK cipher.PubKey
	tpType  types.Type
	tpM     *transport.Manager
	mux     *transport.VStreamMux
	app     *transport.VStreamMux
	log     *logging.Logger
	up      chan struct{}
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

// StartSkynetVia opens a transport of type tpType to visorPK at addr, the
// visor's transport_port (or its stcpr port, or :7777 for stcp), and keeps it
// open until ctx is done. This side registers nothing anywhere and binds its
// own listener to loopback only.
func StartSkynetVia(ctx context.Context, log *logging.Logger, mLog *logging.MasterLogger, pk cipher.PubKey, sk cipher.SecKey, visorPK cipher.PubKey, addr string, tpType types.Type) (*SkynetVia, error) {
	table := stcp.NewTable(map[cipher.PubKey]string{visorPK: addr})
	factory := network.ClientFactory{
		PK:         pk,
		SK:         sk,
		ListenAddr: "127.0.0.1:0",
		EB:         appevent.NewBroadcaster(log, 0),
		MLogger:    mLog,
	}
	switch tpType {
	case types.STCP:
		factory.PKTable = table
	case types.WS:
		factory.WSTable = stcp.NewTable(map[cipher.PubKey]string{visorPK: "ws://" + addr + "/"})
	case types.QUIC:
		factory.QUICTable = table
		factory.ARClient = staticResolver{pk: visorPK, addr: addr}
	case types.STCPR:
		factory.ARClient = staticResolver{pk: visorPK, addr: addr}
	default:
		return nil, fmt.Errorf("skynet via: transport type %q cannot dial a fixed address; use one of %v", tpType, SkynetViaTypes)
	}
	conf := transport.ManagerConfig{
		PubKey:           pk,
		SecKey:           sk,
		DiscoveryClient:  transport.NewDiscoveryMock(),
		LogStore:         transport.InMemoryTransportLogStore(),
		Version:          buildinfo.Version(),
		ARTransportLimit: -1,
	}
	tpM, err := transport.NewManager(log, factory.ARClient, factory.EB, &conf, factory)
	if err != nil {
		return nil, err
	}
	tpM.InitClient(ctx, tpType, 0)
	mux := transport.NewVStreamMux(tpM, routing.SkynetForwardPacket, log)
	tpM.SetSkynetForwardHandler(mux.HandlePacket)
	app := transport.NewVStreamMux(tpM, routing.AppDirectPacket, log)
	tpM.SetAppDirectHandler(app.HandlePacket)
	tpM.Serve(ctx)

	s := &SkynetVia{visorPK: visorPK, tpType: tpType, tpM: tpM, mux: mux, app: app, log: log, up: make(chan struct{})}
	go s.keepTransport(ctx)
	go func() {
		<-ctx.Done()
		_ = mux.Close() //nolint:errcheck
		_ = app.Close() //nolint:errcheck
		tpM.Close()
	}()
	return s, nil
}

func (s *SkynetVia) keepTransport(ctx context.Context) {
	t := time.NewTicker(skynetViaRedial)
	defer t.Stop()
	var upOnce sync.Once
	for {
		if _, err := s.tpM.GetTransport(s.visorPK, s.tpType); err != nil {
			if _, err := s.tpM.SaveTransport(ctx, s.visorPK, s.tpType, transport.LabelUser); err != nil {
				s.log.WithError(err).WithField("visor", s.visorPK.String()).Warn("skynet via: transport to the visor failed")
			} else {
				s.log.WithField("visor", s.visorPK.String()).WithField("type", s.tpType).Info("skynet via: transport to the visor is up")
			}
		}
		if _, err := s.tpM.GetTransport(s.visorPK, s.tpType); err == nil {
			upOnce.Do(func() { close(s.up) })
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

// DmsgClient starts a dmsg client that holds its one session through the
// visor's dmsg relay, over this transport. It registers nothing, and its
// streams go out over the visor's dmsg server sessions under this key.
func (s *SkynetVia) DmsgClient(ctx context.Context, log *logging.Logger, pk cipher.PubKey, sk cipher.SecKey) (*dmsg.Client, func(), error) {
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-s.up:
	}
	dmsgC := dmsg.NewClient(pk, sk, direct.NewClient(nil, log), &dmsg.Config{
		MinSessions: 1,
		RelayOnly:   true,
		NoRegister:  true,
		ClientType:  "attached",
	})
	dmsgC.SetLogger(log)
	dmsgC.SetSessionDialer(func(ctx context.Context, network, addr string) (net.Conn, error) {
		if network != dmsg.CarrierSkynet {
			return nil, fmt.Errorf("skynet via: unsupported carrier %q", network)
		}
		pk, port, err := dmsg.ParseSkynetAddr(addr)
		if err != nil {
			return nil, err
		}
		if pk != s.visorPK {
			return nil, fmt.Errorf("skynet via: relay %s is not the visor", pk)
		}
		conn, err := s.dialVisorPort(port)
		if err != nil {
			// A dial error earns the short relay retry, as the visor restarting is routine.
			return nil, &net.OpError{Op: "dial", Net: dmsg.CarrierSkynet, Err: err}
		}
		return conn, nil
	})
	dmsgC.SetRelayPeers([]cipher.PubKey{s.visorPK}, skyenv.DmsgRelayPort)
	go dmsgC.Serve(ctx)
	stop := func() { _ = dmsgC.Close() } //nolint:errcheck
	select {
	case <-ctx.Done():
		stop()
		return nil, nil, ctx.Err()
	case <-dmsgC.Ready():
		return dmsgC, stop, nil
	}
}

// viaConn gives a VStream the net.Conn methods it lacks.
type viaConn struct{ *transport.VStream }

func (c *viaConn) LocalAddr() net.Addr                { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }
func (c *viaConn) RemoteAddr() net.Addr               { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }
func (c *viaConn) SetDeadline(_ time.Time) error      { return nil }
func (c *viaConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *viaConn) SetWriteDeadline(_ time.Time) error { return nil }

// staticResolver answers address lookups for the one visor at a fixed address
// and binds nothing, standing in for the address resolver.
type staticResolver struct {
	pk   cipher.PubKey
	addr string
}

func (r staticResolver) Resolve(_ context.Context, _ string, pk cipher.PubKey) (addrresolver.VisorData, error) {
	if pk != r.pk {
		return addrresolver.VisorData{}, fmt.Errorf("skynet via: no address for %s", pk)
	}
	return addrresolver.VisorData{RemoteAddr: r.addr}, nil
}

func (staticResolver) BindSTCPR(context.Context, string) error              { return nil }
func (staticResolver) BindQUIC(context.Context, string) error               { return nil }
func (staticResolver) BindWT(context.Context, string, string, string) error { return nil }
func (staticResolver) Transports(context.Context) (map[cipher.PubKey][]string, error) {
	return nil, nil
}
func (staticResolver) TransportsType(context.Context, types.Type) (map[cipher.PubKey][]string, error) {
	return nil, nil
}
func (staticResolver) Addresses(context.Context) string { return "" }
func (staticResolver) LocalPublicIP() string            { return "" }
func (staticResolver) SetPublicIP(string, string)       {}
func (staticResolver) Close() error                     { return nil }

// dialVisorPort opens a direct app stream to a skynet listener on the visor,
// such as its dmsg relay. The wire is a 2-byte port, answered by one ack byte
// that is 0 when the port has a listener.
func (s *SkynetVia) dialVisorPort(port uint16) (net.Conn, error) {
	st, err := s.app.Dial(s.visorPK, "dmsgweb")
	if err != nil {
		return nil, err
	}
	conn := &viaConn{VStream: st}
	hdr := binary.BigEndian.AppendUint16(nil, port)
	ack := make([]byte, 1)
	done := make(chan error, 1)
	go func() {
		if _, err := conn.Write(hdr); err != nil {
			done <- err
			return
		}
		_, err := io.ReadFull(conn, ack)
		done <- err
	}()
	select {
	case err = <-done:
	case <-time.After(skynetViaHandshake):
		err = errors.New("skynet via: no ack from the visor")
	}
	if err == nil && ack[0] != 0 {
		err = fmt.Errorf("skynet via: visor has no listener on port %d", port)
	}
	if err != nil {
		_ = conn.Close() //nolint:errcheck
		return nil, err
	}
	return conn, nil
}
