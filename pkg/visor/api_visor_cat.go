// Package visor api_visor_cat.go — VisorCat RPC method.
//
// VisorCat bridges a remote dmsg / skynet stream to a 127.0.0.1
// loopback TCP listener inside the visor process. The CLI dials that
// loopback and the visor splices the two halves bidirectionally so
// stdio on the CLI side flows to/from the remote peer without the
// bytes ever crossing the local RPC channel.
//
// Two modes:
//
//   - dial: the visor opens the outbound stream and waits for the
//     CLI to dial the returned loopback. Useful for the
//     `cli dmsg cat <pk>:<port>` client.
//   - listen: the visor binds a one-shot listener on the requested
//     remote port (dmsg + skynet mirror), accepts the first inbound
//     stream, authorizes the peer PK against the dmsgscp whitelist,
//     and splices it to a loopback the CLI dials. Used by
//     `cli dmsg cat listen <port>`.
//
// Listener concurrency is one-shot by design — accepting a second
// stream into the same stdout would interleave bytes meaninglessly.
// A persistent server would need its own protocol; we don't have one.
package visor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgpty"
	"github.com/skycoin/skywire/pkg/routing"
)

// Bounds on the loopback wait + remote dial/accept. Dial is short
// because the CLI is racing the visor to the local listener; listen
// is long because operators routinely sit on `cat listen` waiting
// for a peer to call.
const (
	visorCatDefaultDialTimeout   = 60 * time.Second
	visorCatDefaultListenTimeout = 5 * time.Minute
)

// VisorCat opens or accepts a remote stream and returns the loopback
// the CLI should dial. The accept-and-splice work runs in a
// background goroutine so the RPC returns immediately — the CLI's
// loopback dial completes the bridge.
//
// Error categories:
//   - Validation: bad mode / transport / missing remote_pk on dial →
//     wrapped errors the CLI surfaces verbatim.
//   - Dial: dmsg not ready, skynet networker not yet registered,
//     remote unreachable.
//   - Listen: dmsg.Listen / appnet.ListenContext failures, whitelist
//     rejection of the first peer.
func (v *Visor) VisorCat(req VisorCatRequest) (*VisorCatResponse, error) {
	transport := req.Transport
	if transport == "" {
		transport = VisorCatTransportDmsg
	}
	switch transport {
	case VisorCatTransportDmsg, VisorCatTransportSkynet:
	default:
		return nil, fmt.Errorf("VisorCat: bad transport %q (want %q or %q)",
			transport, VisorCatTransportDmsg, VisorCatTransportSkynet)
	}
	if req.Port == 0 {
		return nil, errors.New("VisorCat: port required")
	}

	switch req.Mode {
	case VisorCatModeDial:
		return v.visorCatDial(req, transport)
	case VisorCatModeListen:
		return v.visorCatListen(req, transport)
	default:
		return nil, fmt.Errorf("VisorCat: bad mode %q (want %q or %q)",
			req.Mode, VisorCatModeDial, VisorCatModeListen)
	}
}

// visorCatDial opens the remote stream eagerly (so dial errors surface
// to the CLI's RPC call), then spawns the loopback-listener
// goroutine. The CLI dials the returned LocalAddr to finish the
// splice.
func (v *Visor) visorCatDial(req VisorCatRequest, transport string) (*VisorCatResponse, error) {
	if req.RemotePK.Null() {
		return nil, errors.New("VisorCat: remote_pk required in dial mode")
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = visorCatDefaultDialTimeout
	}

	dialCtx, dialCancel := context.WithTimeout(context.Background(), timeout)
	defer dialCancel()
	remote, err := v.dialCatTransport(dialCtx, transport, req.RemotePK, req.Port)
	if err != nil {
		return nil, fmt.Errorf("VisorCat: dial %s %s:%d: %w",
			transport, req.RemotePK, req.Port, err)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = remote.Close() //nolint:errcheck
		return nil, fmt.Errorf("VisorCat: loopback listen: %w", err)
	}

	go func() {
		defer lis.Close() //nolint:errcheck
		// Bound how long we'll sit on the loopback waiting for the
		// CLI to show up. The RPC call has already returned the
		// LocalAddr; the CLI dials it ~immediately. Timeout here is
		// belt-and-braces against a wedged client.
		acceptCtx, acceptCancel := context.WithTimeout(context.Background(), timeout)
		defer acceptCancel()
		local, accErr := acceptOne(acceptCtx, lis)
		if accErr != nil {
			_ = remote.Close() //nolint:errcheck
			v.log.WithError(accErr).Debug("VisorCat: loopback accept failed")
			return
		}
		splice(local, remote)
	}()

	return &VisorCatResponse{LocalAddr: lis.Addr().String()}, nil
}

// visorCatListen binds the remote-side listener(s) eagerly so the RPC
// returns with the loopback the CLI can dial. The first authorized
// inbound stream gets spliced to the loopback; subsequent attempts
// are rejected and the remote listener closed.
//
// Listen mode always binds BOTH dmsg and skynet listeners on the
// requested port — matching dmsgscp's dual-transport posture — and
// races them. The transport argument is accepted for symmetry with
// the dial-mode path but is otherwise ignored here.
func (v *Visor) visorCatListen(req VisorCatRequest, transport string) (*VisorCatResponse, error) {
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = visorCatDefaultListenTimeout
	}

	wl := v.dmsgWL
	if wl == nil {
		return nil, errors.New("VisorCat: whitelist not initialized (dmsgpty disabled in config)")
	}

	listenCtx, listenCancel := context.WithCancel(context.Background())
	dmsgLis, skyLis, err := v.openCatListener(listenCtx, req.Port)
	if err != nil {
		listenCancel()
		return nil, fmt.Errorf("VisorCat: listen :%d: %w", req.Port, err)
	}
	_ = transport // listen binds both transports; flag is informational only

	loopLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		listenCancel()
		if dmsgLis != nil {
			_ = dmsgLis.Close() //nolint:errcheck
		}
		if skyLis != nil {
			_ = skyLis.Close() //nolint:errcheck
		}
		return nil, fmt.Errorf("VisorCat: loopback listen: %w", err)
	}

	go func() {
		defer listenCancel()
		defer loopLis.Close() //nolint:errcheck
		// Listeners always get torn down on goroutine exit so the
		// dmsg port / skynet route slot is released — even if the CLI
		// gives up before a remote peer arrives, even on timeout, and
		// even on panic. The pre-fix version only closed listeners
		// after acceptCatRemote returned, which could be up to the
		// full --timeout (default 5m) — leaving the dmsg port
		// registered and rejecting future listens as "port already
		// occupied".
		defer func() {
			if dmsgLis != nil {
				_ = dmsgLis.Close() //nolint:errcheck
			}
			if skyLis != nil {
				_ = skyLis.Close() //nolint:errcheck
			}
		}()

		acceptCtx, acceptCancel := context.WithTimeout(context.Background(), timeout)
		defer acceptCancel()

		// Accept the CLI's loopback dial first — it happens within
		// milliseconds of the RPC return. Holding `local` early lets
		// us detect CLI-side give-up (Ctrl-C, exit, timeout) via a
		// sniffer goroutine so the remote accept can be canceled
		// rather than waiting for its own timeout.
		local, err := acceptOne(acceptCtx, loopLis)
		if err != nil {
			v.log.WithError(err).Debug("VisorCat: loopback accept failed")
			return
		}

		// Sniffer: read 1 byte from local. Cases:
		//   - local closes before remote arrives: err signals close,
		//     cancel acceptCtx so remote-wait gives up promptly.
		//   - local sends a byte before remote arrives (CLI piped
		//     stdin early): hold the byte and prepend it to the
		//     splice once remote is ready.
		//   - remote arrives first: signal sniffer to stop; if it
		//     read a byte before stopping, splice prepends.
		type sniffResult struct {
			b   byte
			has bool
			err error
		}
		sniffCh := make(chan sniffResult, 1)
		stopSniff := make(chan struct{})
		go func() {
			var buf [1]byte
			for {
				_ = local.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
				n, rerr := local.Read(buf[:])
				if n > 0 {
					sniffCh <- sniffResult{b: buf[0], has: true}
					return
				}
				if rerr != nil {
					if ne, ok := rerr.(net.Error); ok && ne.Timeout() {
						select {
						case <-stopSniff:
							sniffCh <- sniffResult{}
							return
						default:
							continue
						}
					}
					sniffCh <- sniffResult{err: rerr}
					return
				}
			}
		}()

		// Race remote-accept against sniffer-signals-close.
		type remoteResult struct {
			conn net.Conn
			err  error
		}
		remoteCh := make(chan remoteResult, 1)
		go func() {
			c, e := acceptCatRemote(acceptCtx, wl, dmsgLis, skyLis, v.log)
			remoteCh <- remoteResult{conn: c, err: e}
		}()

		var (
			remote  net.Conn
			pending []byte
		)
	awaitRemote:
		for {
			select {
			case r := <-remoteCh:
				if r.err != nil {
					_ = local.Close() //nolint:errcheck
					close(stopSniff)
					<-sniffCh
					v.log.WithError(r.err).Debug("VisorCat: remote accept failed")
					return
				}
				remote = r.conn
				close(stopSniff)
				s := <-sniffCh
				if s.err != nil {
					// CLI gave up between remote-arrival and sniff-stop;
					// rare race. Tear down remote too.
					_ = remote.Close() //nolint:errcheck
					_ = local.Close() //nolint:errcheck
					return
				}
				if s.has {
					pending = []byte{s.b}
				}
				break awaitRemote
			case s := <-sniffCh:
				if s.err != nil {
					// CLI closed local before remote arrived; cancel
					// remote-wait so the listeners get torn down now.
					acceptCancel()
					_ = local.Close() //nolint:errcheck
					r := <-remoteCh
					if r.conn != nil {
						// Race: remote arrived between our cancel
						// and acceptCatRemote noticing. Don't leak it.
						_ = r.conn.Close() //nolint:errcheck
					}
					return
				}
				if s.has {
					// CLI sent a byte while we were sniffing. Hold it
					// and keep waiting for remote — restart sniffer.
					pending = []byte{s.b}
					go func() {
						var buf [1]byte
						for {
							_ = local.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
							n, rerr := local.Read(buf[:])
							if n > 0 {
								sniffCh <- sniffResult{b: buf[0], has: true}
								return
							}
							if rerr != nil {
								if ne, ok := rerr.(net.Error); ok && ne.Timeout() {
									select {
									case <-stopSniff:
										sniffCh <- sniffResult{}
										return
									default:
										continue
									}
								}
								sniffCh <- sniffResult{err: rerr}
								return
							}
						}
					}()
					// Loop and re-select — may get more bytes or remote.
					// (Buffered bytes accumulate in `pending`.)
					// Continue handles multi-byte CLI-pre-remote streams.
					continue
				}
			}
		}

		// Clear deadline so splice reads block normally.
		_ = local.SetReadDeadline(time.Time{})

		// Drain pending sniff bytes into remote first, then splice.
		if len(pending) > 0 {
			if _, werr := remote.Write(pending); werr != nil {
				_ = local.Close() //nolint:errcheck
				_ = remote.Close() //nolint:errcheck
				v.log.WithError(werr).Debug("VisorCat: drain sniff to remote failed")
				return
			}
		}
		// Listen mode uses half-close semantics: the CLI's stdin is
		// often empty (`< /dev/null` or no piped input), which means
		// the stdin→remote io.Copy returns EOF immediately. With the
		// symmetric splice that would tear down the whole pair before
		// inbound data could arrive. spliceHalfClose keeps the
		// reverse direction open until the remote sender naturally
		// closes their stream.
		spliceHalfClose(local, remote)
	}()

	return &VisorCatResponse{LocalAddr: loopLis.Addr().String()}, nil
}

// dialCatTransport opens a remote stream over the requested transport.
func (v *Visor) dialCatTransport(ctx context.Context, transport string, rPK cipher.PubKey, port uint16) (net.Conn, error) {
	switch transport {
	case VisorCatTransportDmsg:
		if err := v.mustWaitDmsgReady(); err != nil {
			return nil, err
		}
		return v.dmsgC.DialStream(ctx, dmsg.Addr{PK: rPK, Port: port})
	case VisorCatTransportSkynet:
		if _, err := appnet.ResolveNetworker(appnet.TypeSkynet); err != nil {
			return nil, fmt.Errorf("skynet networker not registered: %w", err)
		}
		return appnet.DialContext(ctx, appnet.Addr{
			Net:    appnet.TypeSkynet,
			PubKey: rPK,
			Port:   routing.Port(port),
		})
	}
	return nil, fmt.Errorf("unsupported transport %q", transport)
}

// openCatListener binds BOTH the dmsg and skynet remote-side listeners
// on the requested port and returns them. Mirrors dmsgscp's Host
// posture (dmsg + skynet mirror) so listen mode is reachable over
// whichever transport the peer chooses. Either listener may be nil
// if its underlying stack isn't available — dmsg requires the visor's
// dmsg client to be ready; skynet requires the appnet TypeSkynet
// networker to be registered. At least one must come up; if both
// fail an error is returned.
func (v *Visor) openCatListener(ctx context.Context, port uint16) (net.Listener, net.Listener, error) {
	var (
		dmsgLis net.Listener
		skyLis  net.Listener
		dmsgErr error
		skyErr  error
	)

	if err := v.mustWaitDmsgReady(); err != nil {
		dmsgErr = err
	} else {
		lis, err := v.dmsgC.Listen(port)
		if err != nil {
			dmsgErr = fmt.Errorf("dmsg listen: %w", err)
		} else {
			dmsgLis = lis
		}
	}

	if _, err := appnet.ResolveNetworker(appnet.TypeSkynet); err != nil {
		skyErr = fmt.Errorf("skynet networker not registered: %w", err)
	} else {
		lis, err := appnet.ListenContext(ctx, appnet.Addr{
			Net:    appnet.TypeSkynet,
			PubKey: v.conf.PK,
			Port:   routing.Port(port),
		})
		if err != nil {
			skyErr = fmt.Errorf("skynet listen: %w", err)
		} else {
			skyLis = lis
		}
	}

	if dmsgLis == nil && skyLis == nil {
		return nil, nil, fmt.Errorf("neither transport could bind :%d (dmsg: %v; skynet: %v)", port, dmsgErr, skyErr)
	}
	// Log partial failures at debug — having one transport is enough
	// to serve, but the operator may want to know the other didn't
	// come up.
	if dmsgLis == nil {
		v.log.WithError(dmsgErr).Debugf("VisorCat: dmsg listener on :%d not bound", port)
	}
	if skyLis == nil {
		v.log.WithError(skyErr).Debugf("VisorCat: skynet listener on :%d not bound", port)
	}
	return dmsgLis, skyLis, nil
}

// acceptCatRemote waits for the first stream on either listener (when
// both are non-nil, dmsg and skynet are raced), authorizes the peer
// against wl, and returns the accepted conn. Unauthorized peers are
// closed and the accept loop retries until ctx expires.
func acceptCatRemote(ctx context.Context, wl dmsgpty.Whitelist, dmsgLis, skyLis net.Listener, log logrus.FieldLogger) (net.Conn, error) {
	type acceptResult struct {
		conn net.Conn
		err  error
	}
	// Buffered enough for both listeners to push without blocking.
	results := make(chan acceptResult, 2)

	var wg sync.WaitGroup
	spawn := func(lis net.Listener) {
		if lis == nil {
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				conn, err := lis.Accept()
				if err != nil {
					select {
					case results <- acceptResult{err: err}:
					case <-ctx.Done():
					}
					return
				}
				rPK, ok := remotePK(conn.RemoteAddr())
				if !ok {
					_ = conn.Close() //nolint:errcheck
					continue
				}
				allowed, wlErr := wl.Get(rPK)
				if wlErr != nil || !allowed {
					if log != nil {
						log.WithField("remote_pk", rPK.String()).
							Warn("VisorCat: peer rejected by whitelist")
					}
					_ = conn.Close() //nolint:errcheck
					continue
				}
				select {
				case results <- acceptResult{conn: conn}:
				case <-ctx.Done():
					_ = conn.Close() //nolint:errcheck
				}
				return
			}
		}()
	}
	spawn(dmsgLis)
	spawn(skyLis)

	select {
	case r := <-results:
		// Drain any straggler accept result so its goroutine exits.
		go func() {
			wg.Wait()
		}()
		if r.err != nil {
			return nil, r.err
		}
		return r.conn, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// acceptOne accepts a single conn from lis, returning early when ctx
// is canceled. Closes the listener on ctx cancel so a pending Accept
// unblocks with ErrClosed.
func acceptOne(ctx context.Context, lis net.Listener) (net.Conn, error) {
	type acceptResult struct {
		conn net.Conn
		err  error
	}
	ch := make(chan acceptResult, 1)
	go func() {
		conn, err := lis.Accept()
		ch <- acceptResult{conn: conn, err: err}
	}()
	select {
	case r := <-ch:
		return r.conn, r.err
	case <-ctx.Done():
		_ = lis.Close() //nolint:errcheck
		// Drain so the goroutine doesn't leak.
		r := <-ch
		if r.conn != nil {
			_ = r.conn.Close() //nolint:errcheck
		}
		return nil, ctx.Err()
	}
}

// splice runs bidirectional io.Copy between a and b. Returns when
// either direction errors or hits EOF, closing both ends. Suitable
// for any pair of net.Conn — dmsg.Stream, appnet route-group conn,
// loopback TCP — they all satisfy the interface.
//
// Symmetric full-close on first EOF. Use this for the dial side
// where a stdin EOF means "sender done — tear it all down". The
// listener side wants asymmetric half-close semantics; see
// spliceHalfClose.
//
// Returns the first non-EOF error seen, or nil on a clean EOF.
func splice(a, b net.Conn) error {
	var (
		once    sync.Once
		closeFn = func() {
			_ = a.Close() //nolint:errcheck
			_ = b.Close() //nolint:errcheck
		}
	)
	type copyResult struct{ err error }
	done := make(chan copyResult, 2)
	go func() {
		_, err := io.Copy(a, b)
		once.Do(closeFn)
		done <- copyResult{err: err}
	}()
	go func() {
		_, err := io.Copy(b, a)
		once.Do(closeFn)
		done <- copyResult{err: err}
	}()
	first := <-done
	<-done
	if first.err == nil || errors.Is(first.err, io.EOF) || errors.Is(first.err, net.ErrClosed) {
		return nil
	}
	return first.err
}

// closeWriter is the optional half-close interface satisfied by
// *net.TCPConn (and *net.UnixConn). dmsg.Stream and appnet
// route-group conns don't implement it — we fall back to "wait for
// the reverse direction to also EOF before full Close" there.
type closeWriter interface {
	CloseWrite() error
}

// spliceHalfClose is the asymmetric variant of splice used by the
// listen mode. When one direction of io.Copy returns (its source
// EOFed), we DO NOT close the whole pair. Instead:
//
//   - If the destination supports CloseWrite, we CloseWrite there to
//     signal "no more bytes from this side" without tearing down the
//     reverse direction.
//   - If the destination doesn't (dmsg.Stream / appnet conn), we
//     do nothing — wait for the reverse direction to also reach EOF
//     before doing the full pair-Close.
//
// Why: the listen-mode CLI's local stdin is often `< /dev/null` or
// an interactive terminal that never produces bytes; without
// half-close awareness the immediate stdin-EOF would tear down the
// whole splice before any inbound data could be received. With this
// variant the receive direction keeps flowing until the remote sender
// closes their side naturally.
//
// Hazard: if BOTH peers happen to have empty stdin AND no
// CloseWrite-capable destination (i.e., both pairs are dmsg/dmsg),
// neither direction will ever EOF and the splice will hang. In
// practice one half of the pair is always a TCP loopback (the
// CLI-to-visor bridge) which DOES support CloseWrite, so the cascade
// completes once one side finishes.
func spliceHalfClose(a, b net.Conn) error {
	type copyResult struct{ err error }
	done := make(chan copyResult, 2)
	go func() {
		_, err := io.Copy(a, b)
		// b is exhausted. Signal a that no more bytes are coming
		// from b, but keep a's read half open so the reverse
		// direction can still drain.
		if cw, ok := a.(closeWriter); ok {
			_ = cw.CloseWrite() //nolint:errcheck
		}
		done <- copyResult{err: err}
	}()
	go func() {
		_, err := io.Copy(b, a)
		if cw, ok := b.(closeWriter); ok {
			_ = cw.CloseWrite() //nolint:errcheck
		}
		done <- copyResult{err: err}
	}()
	r1 := <-done
	r2 := <-done
	_ = a.Close() //nolint:errcheck
	_ = b.Close() //nolint:errcheck
	for _, r := range []copyResult{r1, r2} {
		if r.err != nil && !errors.Is(r.err, io.EOF) && !errors.Is(r.err, net.ErrClosed) {
			return r.err
		}
	}
	return nil
}
