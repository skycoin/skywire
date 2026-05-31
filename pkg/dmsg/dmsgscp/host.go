// Package dmsgscp pkg/dmsg/dmsgscp/host.go: the listening side of
// scp-over-dmsg. Host accepts dmsg streams on its configured port,
// authorizes the remote PK against an injected Whitelist, reads the
// first line to discover the role the caller wants the host to play
// (SOURCE = caller downloads, SINK = caller uploads), then drives
// the framing accordingly.
//
// The structure deliberately mirrors pkg/dmsg/dmsgpty/host.go — same
// accept loop, same temporary-error retry, same close hooks. The
// big difference is the dispatch: dmsgpty multiplexes RPC endpoints
// over a single yamux-ish mux on each stream; dmsgscp uses each
// stream for a single file transfer end-to-end.
package dmsgscp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/pty"
)

// Whitelist is an alias for pty.Whitelist so operators can pass
// the same whitelist instance to both daemons without re-importing.
// We deliberately don't define a new interface here — the contract
// is identical and the indirection would just confuse callers.
type Whitelist = pty.Whitelist

// Host is the listening side of dmsgscp.
type Host struct {
	dmsgC   *dmsg.Client
	wl      Whitelist
	rootDir string

	connN int32
}

// NewHost creates a new dmsgscp Host with the given dmsg client,
// whitelist, and filesystem root. rootDir is created with 0700
// permissions if it does not already exist — the directory is the
// effective chroot for every served transfer.
func NewHost(dmsgC *dmsg.Client, wl Whitelist, rootDir string) (*Host, error) {
	if dmsgC == nil {
		return nil, errors.New("dmsgscp: nil dmsg client")
	}
	if wl == nil {
		return nil, errors.New("dmsgscp: nil whitelist")
	}
	if rootDir == "" {
		return nil, errors.New("dmsgscp: empty root directory")
	}
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("dmsgscp: resolve root dir: %w", err)
	}
	if err := os.MkdirAll(absRoot, 0700); err != nil {
		return nil, fmt.Errorf("dmsgscp: create root dir: %w", err)
	}
	return &Host{
		dmsgC:   dmsgC,
		wl:      wl,
		rootDir: absRoot,
	}, nil
}

// RootDir returns the filesystem root the host serves from.
func (h *Host) RootDir() string { return h.rootDir }

// ListenAndServe binds the configured dmsg port and serves transfers
// until ctx is canceled or the listener fails permanently. The
// signature and accept-loop pattern intentionally mirror
// pty.Host.ListenAndServe — see that function's comments for
// the rationale on the temporary-error sleep and the close-pipe
// classification.
//
// The accept loop body is shared with ServeListener so the same Host
// can also accept on a skynet listener for dual-transport parity.
func (h *Host) ListenAndServe(ctx context.Context, port uint16) error {
	lis, err := h.dmsgC.Listen(port)
	if err != nil {
		return fmt.Errorf("dmsgscp: listen on dmsg port %d: %w", port, err)
	}
	h.logger().WithField("port", port).WithField("root", h.rootDir).
		Debug("dmsgscp listening on dmsg.")
	return h.ServeListener(ctx, lis)
}

// ServeListener runs the accept loop against a caller-provided
// listener. Used directly by the skynet-mirror goroutine in
// initDmsgpty so the dmsgscp Host serves over BOTH dmsg and the
// skywire router at the same port number. The listener is closed
// when ctx is canceled.
//
// Authorization (whitelist check) happens per-stream and works
// uniformly across listener types because remoteAddrToPK handles
// both dmsg.Addr and appnet.Addr shapes.
func (h *Host) ServeListener(ctx context.Context, lis net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	log := h.logger()

	go func() {
		<-ctx.Done()
		log.WithError(lis.Close()).Debug("dmsgscp listener closed.")
	}()

	for {
		conn, err := lis.Accept()
		if err != nil {
			log := log.WithError(err)
			// Same temporary-error treatment as pty.
			if err, ok := err.(net.Error); ok && err.Temporary() { //nolint
				log.Warn("dmsgscp: temporary accept error, continuing...")
				time.Sleep(50 * time.Millisecond)
				continue
			}
			if errors.Is(err, io.ErrClosedPipe) ||
				errors.Is(err, dmsg.ErrEntityClosed) ||
				errors.Is(err, net.ErrClosed) {
				log.Debug("dmsgscp: cleanly stopped serving.")
				return nil
			}
			log.Error("dmsgscp: permanent accept error.")
			return err
		}

		rPK, ok := remoteAddrToPK(conn.RemoteAddr())
		if !ok {
			log.WithField("addr_type", fmt.Sprintf("%T", conn.RemoteAddr())).
				Warn("dmsgscp: cannot extract peer PK from accepted conn; closing.")
			_ = conn.Close() //nolint:errcheck
			continue
		}
		slog := log.WithField("remote_pk", rPK.String())

		if !h.authorize(slog, rPK) {
			// Tell the peer why we're hanging up so the client
			// surface gets a useful error rather than a bare EOF.
			if werr := WriteFatal(conn, "dmsg stream rejected by whitelist"); werr != nil {
				slog.WithError(werr).Debug("dmsgscp: failed to write rejection.")
			}
			if cerr := conn.Close(); cerr != nil {
				slog.WithError(cerr).Debug("dmsgscp: error closing rejected stream.")
			}
			continue
		}

		slog = slog.WithField("conn_id", atomic.AddInt32(&h.connN, 1))
		slog.Debug("dmsgscp: stream accepted.")
		go func(s net.Conn, l logrus.FieldLogger) {
			h.serve(ctx, l, s)
			atomic.AddInt32(&h.connN, -1)
		}(conn, slog)
	}
}

// remoteAddrToPK extracts a cipher.PubKey from an accepted conn's
// RemoteAddr. Supports dmsg.Addr (dmsg listener) and appnet.Addr
// (skynet listener via the skywire router). Unrecognized address
// types return ok=false; the caller logs + closes the connection.
func remoteAddrToPK(addr net.Addr) (cipher.PubKey, bool) {
	switch a := addr.(type) {
	case dmsg.Addr:
		return a.PK, true
	case appnet.Addr:
		return a.PubKey, true
	}
	return cipher.PubKey{}, false
}

// authorize mirrors the dmsgpty implementation byte-for-byte so the
// audit story stays identical between the two daemons.
func (h *Host) authorize(log logrus.FieldLogger, rPK cipher.PubKey) bool {
	ok, err := h.wl.Get(rPK)
	if err != nil {
		log.WithError(err).Error("dmsgscp: whitelist error.")
		return false
	}
	if !ok {
		log.Warn("dmsgscp: public key rejected by whitelist.")
		return false
	}
	return true
}

// serve handles one accepted stream end-to-end. It reads the role
// line, dispatches to serveSource (host reads file, sends to client)
// or serveSink (host receives file from client), and tears the
// stream down on exit. Any unexpected I/O failure is logged at debug
// — the client side will surface its own error.
func (h *Host) serve(ctx context.Context, log logrus.FieldLogger, conn net.Conn) {
	defer func() {
		if err := conn.Close(); err != nil {
			log.WithError(err).Debug("dmsgscp: stream close error.")
		}
	}()

	r := bufio.NewReader(conn)
	line, err := readRoleLine(r)
	if err != nil {
		log.WithError(err).Debug("dmsgscp: failed to read role line.")
		_ = WriteFatal(conn, "malformed role line") //nolint:errcheck
		return
	}

	role, rawPath, err := parseRoleLine(line)
	if err != nil {
		log.WithError(err).WithField("line", line).Warn("dmsgscp: bad role line.")
		_ = WriteFatal(conn, err.Error()) //nolint:errcheck
		return
	}

	switch role {
	case RoleSource:
		h.serveSource(ctx, log, conn, r, rawPath)
	case RoleSink:
		h.serveSink(ctx, log, conn, r, rawPath)
	default:
		// Already filtered by parseRoleLine, but keep the switch
		// total so a future addition gets caught.
		_ = WriteFatal(conn, "unknown role") //nolint:errcheck
	}
}

// serveSource handles a SOURCE request — the host plays the role of
// the file sender. The wire flow is:
//
//  1. peer dialed and sent  `SOURCE <path>\n`
//  2. host writes `C<mode> <size> <name>\n`
//  3. host reads ack \x00
//  4. host writes <size> bytes
//  5. host writes ack \x00
//  6. host reads ack \x00
//
// Any failure path writes a fatal-prefix line so the client gets a
// human-readable reason, then returns.
func (h *Host) serveSource(_ context.Context, log logrus.FieldLogger, conn net.Conn, r *bufio.Reader, name string) {
	safePath, err := ResolveSafePath(h.rootDir, name)
	if err != nil {
		log.WithError(err).WithField("name", name).Warn("dmsgscp: path resolution failed.")
		_ = WriteFatal(conn, "bad path: "+err.Error()) //nolint:errcheck
		return
	}

	info, err := os.Stat(safePath)
	if err != nil {
		log.WithError(err).Debug("dmsgscp: stat failed.")
		_ = WriteFatal(conn, "stat: "+err.Error()) //nolint:errcheck
		return
	}
	if info.IsDir() {
		_ = WriteFatal(conn, "directory transfers not supported") //nolint:errcheck
		return
	}

	f, err := os.Open(safePath) //nolint:gosec // path validated by ResolveSafePath
	if err != nil {
		_ = WriteFatal(conn, "open: "+err.Error()) //nolint:errcheck
		return
	}
	defer f.Close() //nolint:errcheck

	if err := WriteFileHeader(conn, info.Mode(), info.Size(), filepath.Base(name)); err != nil {
		log.WithError(err).Debug("dmsgscp: WriteFileHeader failed.")
		return
	}

	if msg, err := ReadAck(r); err != nil {
		log.WithError(err).WithField("msg", string(msg)).Debug("dmsgscp: peer did not ack header.")
		return
	}

	// io.CopyN exactly the claimed size — guards against runaway
	// writes if Stat lied between the header write and the read.
	if _, err := io.CopyN(conn, f, info.Size()); err != nil {
		log.WithError(err).Debug("dmsgscp: payload copy failed.")
		return
	}

	if err := WriteAck(conn); err != nil {
		log.WithError(err).Debug("dmsgscp: final ack write failed.")
		return
	}

	if msg, err := ReadAck(r); err != nil {
		log.WithError(err).WithField("msg", string(msg)).Debug("dmsgscp: peer did not ack payload.")
		return
	}

	log.WithField("path", safePath).WithField("bytes", info.Size()).Info("dmsgscp: SOURCE complete.")
}

// serveSink handles a SINK request — the host plays the role of the
// file receiver. The wire flow is:
//
//  1. peer dialed and sent  `SINK <path>\n`
//  2. host writes ack \x00 (ready)
//  3. peer writes `C<mode> <size> <name>\n`
//  4. host writes ack \x00
//  5. peer writes <size> bytes
//  6. peer writes ack \x00
//  7. host writes ack \x00
//
// The `<name>` in the C-record is treated as the basename only — the
// destination directory comes from the SINK path. This matches the
// rcp/scp convention: the caller specifies the destination on dial.
func (h *Host) serveSink(_ context.Context, log logrus.FieldLogger, conn net.Conn, r *bufio.Reader, name string) {
	safePath, err := ResolveSafePath(h.rootDir, name)
	if err != nil {
		log.WithError(err).WithField("name", name).Warn("dmsgscp: path resolution failed.")
		_ = WriteFatal(conn, "bad path: "+err.Error()) //nolint:errcheck
		return
	}

	// Ensure the parent dir exists so a SINK request can place files
	// in arbitrary depth under rootDir without the operator having
	// to pre-create the tree.
	if err := os.MkdirAll(filepath.Dir(safePath), 0700); err != nil {
		_ = WriteFatal(conn, "mkdir: "+err.Error()) //nolint:errcheck
		return
	}

	// Tell the peer we're ready to read the C-record.
	if err := WriteAck(conn); err != nil {
		log.WithError(err).Debug("dmsgscp: ready-ack write failed.")
		return
	}

	hdr, err := ReadHeader(r)
	if err != nil {
		log.WithError(err).Debug("dmsgscp: ReadHeader failed.")
		_ = WriteFatal(conn, "bad header: "+err.Error()) //nolint:errcheck
		return
	}
	if hdr.Type == RecordDirStart || hdr.Type == RecordDirEnd {
		_ = WriteFatal(conn, ErrDirNotSupported.Error()) //nolint:errcheck
		return
	}
	if hdr.Type != RecordFile {
		_ = WriteFatal(conn, "unexpected record type") //nolint:errcheck
		return
	}

	if err := WriteAck(conn); err != nil {
		log.WithError(err).Debug("dmsgscp: header-ack write failed.")
		return
	}

	// Open the destination O_TRUNC so a repeat upload overwrites
	// cleanly. Permissions come from the wire-supplied mode masked
	// by 0700 (umask-style) so we never grant world access via a
	// malicious upload.
	mode := hdr.Mode & 0700
	if mode == 0 {
		mode = 0600
	}
	f, err := os.OpenFile(safePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode) //nolint:gosec // path validated
	if err != nil {
		_ = WriteFatal(conn, "open: "+err.Error()) //nolint:errcheck
		return
	}
	// Close hooks here matter — io.CopyN below can fail mid-write
	// and we want the partial file to be removed rather than left
	// as a confusing artifact.
	cleanup := func(success bool) {
		if cerr := f.Close(); cerr != nil {
			log.WithError(cerr).Debug("dmsgscp: file close error.")
		}
		if !success {
			if rerr := os.Remove(safePath); rerr != nil && !os.IsNotExist(rerr) {
				log.WithError(rerr).Debug("dmsgscp: failed to remove partial upload.")
			}
		}
	}

	if _, err := io.CopyN(f, r, hdr.Size); err != nil {
		log.WithError(err).Debug("dmsgscp: payload copy failed.")
		cleanup(false)
		return
	}

	// Read the sender's trailing payload ack.
	if msg, err := ReadAck(r); err != nil {
		log.WithError(err).WithField("msg", string(msg)).Debug("dmsgscp: peer did not send trailing ack.")
		cleanup(false)
		return
	}

	if err := WriteAck(conn); err != nil {
		log.WithError(err).Debug("dmsgscp: final ack write failed.")
		cleanup(false)
		return
	}

	cleanup(true)
	log.WithField("path", safePath).WithField("bytes", hdr.Size).Info("dmsgscp: SINK complete.")
}

// readRoleLine reads the first line from a freshly-dialed stream.
// We use a bounded line length: a malicious peer could otherwise
// stall the accept loop by trickling header bytes forever. The
// 4 KiB default bufio buffer is more than enough — a role line is
// `SOURCE ` plus a path bounded by MaxNameLen.
func readRoleLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	if len(line) > MaxHeaderLen {
		return "", ErrHeaderTooLong
	}
	// Strip trailing \r\n / \n robustly.
	line = strings.TrimRight(line, "\r\n")
	return line, nil
}

// parseRoleLine splits `<ROLE> <path>` and returns the canonical
// role + raw path. Returns a descriptive error suitable for sending
// back over the wire on failure.
func parseRoleLine(line string) (role, path string, err error) {
	// SplitN(2) so a path containing spaces survives.
	parts := strings.SplitN(line, " ", 2)
	if len(parts) != 2 {
		return "", "", errors.New("expected `SOURCE <path>` or `SINK <path>`")
	}
	role = parts[0]
	path = parts[1]
	if role != RoleSource && role != RoleSink {
		return "", "", fmt.Errorf("unknown role %q", role)
	}
	if path == "" {
		return "", "", errors.New("empty path")
	}
	return role, path, nil
}

// logger returns the field-logger used for all server-side log lines.
func (h *Host) logger() logrus.FieldLogger {
	if ml := h.dmsgC.MasterLogger(); ml != nil {
		return ml.PackageLogger("dmsg_scp")
	}
	return logging.MustGetLogger("dmsg_scp")
}
