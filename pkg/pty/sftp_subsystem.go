// Package pty pkg/pty/sftp_subsystem.go — server-side sftp subsystem
// that rides the same noise-protected stream and whitelist auth as the
// pty subsystem, dispatched by SftpURI.
//
// Trust model: the conn arriving here has ALREADY been authorized by
// h.authorize() in the listening loop (ListenAndServe / ListenAndServeNet
// / ListenAndServeTCP). The remote PK is on the dmsgpty whitelist; that
// is the same gate as the interactive-pty path. There is no separate
// sftp ACL by design — operators who trust a peer for shell access
// implicitly trust them for filesystem access, mirroring OpenSSH's
// default where sshd grants both unless explicitly subsystem-restricted.
//
// Mount-side concerns (read-only, chroot, allow-list of paths) are out
// of scope for v1 and belong in a follow-up that adds subsystem-level
// policy hooks shared between exec/pty/sftp. Until then the served root
// is the host process's filesystem view as it would be over ssh; the
// dmsgpty_whitelist gate is the operator's lever.
package pty

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"

	"github.com/pkg/sftp"

	"github.com/skycoin/skywire/pkg/logging"
)

// handleSftp returns the connHandleFunc registered against SftpURI.
// Each accepted conn becomes its own sftp.Server bound to the
// underlying stream — the server runs until the client closes or an
// I/O error tears the stream down, then returns. Errors are logged at
// debug; a torn stream is not a hot-loop concern, the listener loop
// hands the next conn its own server.
func handleSftp(h *Host) connHandleFunc {
	return func(ctx context.Context, _ *url.URL, conn net.Conn) error {
		log := logging.MustGetLogger("dmsgpty:sftp")
		if h.dmsgC != nil {
			if ml := h.dmsgC.MasterLogger(); ml != nil {
				log = ml.PackageLogger("dmsgpty:sftp")
			}
		}

		srv, err := sftp.NewServer(conn)
		if err != nil {
			log.WithError(err).Warn("sftp: NewServer failed.")
			return err
		}

		// Close srv on ctx-cancel so a host shutdown tears the session
		// down deterministically. srv.Serve returns io.EOF on clean
		// client-side close; ignore it the same way OpenSSH's sftp
		// server treats EOF as a normal session end.
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				_ = srv.Close() //nolint:errcheck
			case <-done:
			}
		}()
		defer close(done)

		if err := srv.Serve(); err != nil && !errors.Is(err, io.EOF) {
			log.WithError(err).Debug("sftp: Serve returned with error.")
			return err
		}
		return nil
	}
}
