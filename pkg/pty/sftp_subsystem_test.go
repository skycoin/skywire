package pty

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/pkg/sftp"
)

// TestMux_BackwardsCompat_RejectsUnknownURI confirms the URI-as-
// subsystem dispatch keeps backwards-compat: an older server with no
// SftpURI registration replies "invalid request" to a new client's
// SftpURI request, exactly like any other unrecognized URI. No
// hidden version bumping; no silent acceptance.
//
// An empty mux is the simplest faithful model of an "old server"
// for this dispatch test — the URI dispatch logic is the same
// regardless of what other URIs the real production mux registers.
func TestMux_BackwardsCompat_RejectsUnknownURI(t *testing.T) {
	mux := hostMux{}

	srvConn, cliConn := net.Pipe()
	defer srvConn.Close() //nolint:errcheck,gosec
	defer cliConn.Close() //nolint:errcheck,gosec

	go func() {
		_ = mux.ServeConn(context.Background(), srvConn) //nolint:errcheck
	}()

	if err := writeRequest(cliConn, SftpURI); err != nil {
		t.Fatalf("writeRequest: %v", err)
	}
	if err := readResponse(cliConn); err == nil {
		t.Fatalf("expected rejection from old server on SftpURI, got nil")
	}
}

// TestMux_SftpURI_DispatchedToConnHandler confirms the new conn-
// dispatch path: after the URI handshake the conn lands in the
// raw-conn handler with NO net/rpc layer between, ready for an
// arbitrary subsystem protocol.
func TestMux_SftpURI_DispatchedToConnHandler(t *testing.T) {
	mux := hostMux{}

	gotConn := make(chan struct{}, 1)
	handler := func(_ context.Context, _ *url.URL, c net.Conn) error {
		gotConn <- struct{}{}
		// Echo one round-trip so the test can verify the conn is live
		// and positioned past the URI handshake (i.e. the next byte
		// the client writes is the next byte the handler reads).
		buf := make([]byte, 5)
		if _, err := io.ReadFull(c, buf); err != nil {
			return err
		}
		_, err := c.Write(buf)
		return err
	}
	if err := mux.HandleConn(SftpURI, handler); err != nil {
		t.Fatalf("HandleConn: %v", err)
	}

	srvConn, cliConn := net.Pipe()
	defer srvConn.Close() //nolint:errcheck,gosec
	defer cliConn.Close() //nolint:errcheck,gosec

	go func() {
		_ = mux.ServeConn(context.Background(), srvConn) //nolint:errcheck
	}()

	if err := writeRequest(cliConn, SftpURI); err != nil {
		t.Fatalf("writeRequest: %v", err)
	}
	if err := readResponse(cliConn); err != nil {
		t.Fatalf("readResponse: %v", err)
	}

	select {
	case <-gotConn:
	case <-time.After(2 * time.Second):
		t.Fatalf("conn handler never fired")
	}

	if _, err := cliConn.Write([]byte("hello")); err != nil {
		t.Fatalf("client write: %v", err)
	}
	echo := make([]byte, 5)
	if _, err := io.ReadFull(cliConn, echo); err != nil {
		t.Fatalf("client read: %v", err)
	}
	if string(echo) != "hello" {
		t.Fatalf("echo mismatch: got %q want %q", echo, "hello")
	}
}

// TestSftpSubsystem_EndToEnd drives a real sftp.Client through a real
// in-process sftp.Server registered via the dmsgpty mux. Verifies a
// minimum-viable Stat across the boundary so we know the URI
// handshake leaves the conn positioned correctly for the sftp byte
// protocol — the most common failure mode of adding a subsystem to
// an existing mux is "first byte of subsystem data got consumed by
// the handshake reader" and Stat is enough to catch it.
func TestSftpSubsystem_EndToEnd(t *testing.T) {
	mux := hostMux{}

	// Inline equivalent of the real handleSftp without needing a
	// *Host (which requires a dmsg.Client to construct). Wire shape
	// is identical.
	if err := mux.HandleConn(SftpURI, func(_ context.Context, _ *url.URL, c net.Conn) error {
		srv, err := sftp.NewServer(c)
		if err != nil {
			return err
		}
		err = srv.Serve()
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("HandleConn: %v", err)
	}

	srvConn, cliConn := net.Pipe()

	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		_ = mux.ServeConn(context.Background(), srvConn) //nolint:errcheck
	}()

	if err := writeRequest(cliConn, SftpURI); err != nil {
		t.Fatalf("writeRequest: %v", err)
	}
	if err := readResponse(cliConn); err != nil {
		t.Fatalf("readResponse: %v", err)
	}

	sc, err := sftp.NewClientPipe(cliConn, cliConn)
	if err != nil {
		t.Fatalf("sftp.NewClientPipe: %v", err)
	}

	fi, err := sc.Stat("/")
	if err != nil {
		t.Fatalf("sftp.Stat /: %v", err)
	}
	if !fi.IsDir() {
		t.Fatalf("expected / to be a directory, got %v", fi.Mode())
	}

	if err := sc.Close(); err != nil {
		t.Fatalf("sftp client close: %v", err)
	}
	_ = cliConn.Close() //nolint:errcheck
	_ = srvConn.Close() //nolint:errcheck

	select {
	case <-serveDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("mux.ServeConn never returned after client close")
	}
}
