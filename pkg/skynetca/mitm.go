package skynetca

import (
	"crypto/tls"
	"io"
	"net"
)

// MITMTerminate accepts an upstream net.Conn (the skywire transport
// to a merchant visor's plaintext HTTP server) and returns a
// net.Conn the caller (typically a SOCKS5 server's Dial callback)
// can splice against the browser.
//
// Internally:
//
//  1. A net.Pipe is created.
//  2. One end is returned to the caller.
//  3. The other end is wrapped in tls.Server with leaf and the TLS
//     handshake is run with the browser via the pipe.
//  4. After handshake, plaintext is spliced bidirectionally between
//     the tls.Conn and upstream.
//
// On any failure (handshake error, splice error, upstream close)
// all resources are released. The function returns immediately;
// the splice runs on a background goroutine. The caller must close
// the returned net.Conn when done; this triggers teardown of the
// goroutine and the upstream conn.
func MITMTerminate(upstream net.Conn, leaf *tls.Certificate) net.Conn {
	a, b := net.Pipe()
	cfg := &tls.Config{
		Certificates: []tls.Certificate{*leaf},
		MinVersion:   tls.VersionTLS12,
	}
	tlsConn := tls.Server(b, cfg)
	go func() {
		defer upstream.Close() //nolint:errcheck,gosec
		// Closing tlsConn closes b too.
		defer tlsConn.Close() //nolint:errcheck,gosec

		// Run the handshake explicitly so handshake errors don't
		// race with the io.Copy goroutines below.
		if err := tlsConn.Handshake(); err != nil {
			return
		}
		errC := make(chan error, 2)
		go func() {
			_, e := io.Copy(upstream, tlsConn)
			errC <- e
		}()
		go func() {
			_, e := io.Copy(tlsConn, upstream)
			errC <- e
		}()
		<-errC
	}()
	return a
}
