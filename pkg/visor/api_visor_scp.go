// Package visor api_visor_scp.go — VisorSCP RPC method.
//
// VisorSCP performs a dmsgscp-protocol file transfer from inside
// the visor process. The visor has both transports plumbed
// (dmsg.Client + appnet SkywireNetworker), where a standalone CLI
// has only the dmsg path. Routing the SCP through the visor lets
// callers pick either transport without bolting an appnet shim
// onto the CLI.
//
// Lives in its own file so the dmsgscp / appnet imports stay
// scoped to one compilation unit.
package visor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgscp"
	"github.com/skycoin/skywire/pkg/routing"
)

// visorSCPDefaultTimeout bounds the whole transfer (dial + scp
// protocol + payload) when the caller doesn't supply one. Five
// minutes is comfortable for a few-hundred-MB binary over a
// well-routed transport without being so generous that a wedged
// stream sits forever.
const visorSCPDefaultTimeout = 5 * time.Minute

// VisorSCP runs a dmsgscp transfer to req.RemotePK:req.Port over
// req.Transport ("dmsg" or "skynet"), uploading or downloading per
// req.Direction. The local-filesystem path req.LocalPath is read
// or written by THIS visor process — operators running the CLI
// against a different visor see that visor's filesystem, not the
// caller's.
//
// Error categories:
//   - Validation: bad PK / direction / transport / empty paths →
//     wrapped errors with stable prefixes the CLI can surface.
//   - Dial: dmsg session not ready, or skynet networker not yet
//     registered when initRouter raced this call.
//   - Protocol: the dmsgscp.Client surface returns the same
//     fmt.Errorf-wrapped sentinels the standalone client does.
func (v *Visor) VisorSCP(req VisorSCPRequest) error {
	if req.RemotePK.Null() {
		return errors.New("VisorSCP: remote_pk required")
	}
	if req.Port == 0 {
		req.Port = dmsgscp.DefaultPort
	}
	if req.LocalPath == "" {
		return errors.New("VisorSCP: local_path required")
	}
	if req.RemotePath == "" {
		return errors.New("VisorSCP: remote_path required")
	}
	switch req.Direction {
	case VisorSCPUpload, VisorSCPDownload:
	default:
		return fmt.Errorf("VisorSCP: bad direction %q (want %q or %q)",
			req.Direction, VisorSCPUpload, VisorSCPDownload)
	}
	transport := req.Transport
	if transport == "" {
		transport = VisorSCPTransportDmsg
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = visorSCPDefaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var (
		conn net.Conn
		err  error
	)
	switch transport {
	case VisorSCPTransportDmsg:
		conn, err = v.dialSCPDmsg(ctx, req.RemotePK, req.Port)
	case VisorSCPTransportSkynet:
		conn, err = v.dialSCPSkynet(ctx, req.RemotePK, req.Port)
	default:
		return fmt.Errorf("VisorSCP: bad transport %q (want %q or %q)",
			transport, VisorSCPTransportDmsg, VisorSCPTransportSkynet)
	}
	if err != nil {
		return fmt.Errorf("VisorSCP: dial %s %s:%d: %w",
			transport, req.RemotePK, req.Port, err)
	}
	client := dmsgscp.NewClient(conn, req.RemotePK)
	defer client.Close() //nolint:errcheck,gosec

	switch req.Direction {
	case VisorSCPUpload:
		return client.Upload(req.LocalPath, req.RemotePath)
	case VisorSCPDownload:
		return client.Download(req.RemotePath, req.LocalPath)
	}
	// Unreachable — direction validated above.
	return nil
}

// dialSCPDmsg opens a dmsg.Stream to (rPK:port). Reuses the visor's
// existing dmsg.Client (v.dmsgC) so we share sessions with every
// other dmsg consumer instead of paying a fresh handshake.
func (v *Visor) dialSCPDmsg(ctx context.Context, rPK cipher.PubKey, port uint16) (net.Conn, error) {
	if err := v.mustWaitDmsgReady(); err != nil {
		return nil, err
	}
	return v.dmsgC.DialStream(ctx, dmsg.Addr{PK: rPK, Port: port})
}

// dialSCPSkynet opens a route-group stream to (rPK:port) via the
// SkywireNetworker. Goes straight to addr.Port (no
// SkyForwardingServerPort handshake) because the peer's dmsgscp
// host binds its appnet listener directly on its visor port via
// goServeSkynetMirror in init_dmsg.go.
//
// The dial blocks until either the route group is established or
// the caller's deadline fires. Errors propagate verbatim from the
// router — the wrapping VisorSCP call layers in "skynet PK:port"
// context for the CLI to surface.
func (v *Visor) dialSCPSkynet(ctx context.Context, rPK cipher.PubKey, port uint16) (net.Conn, error) {
	if _, err := appnet.ResolveNetworker(appnet.TypeSkynet); err != nil {
		return nil, fmt.Errorf("skynet networker not registered: %w", err)
	}
	return appnet.DialContext(ctx, appnet.Addr{
		Net:    appnet.TypeSkynet,
		PubKey: rPK,
		Port:   routing.Port(port),
	})
}
