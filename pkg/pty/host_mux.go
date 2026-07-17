// Package pty pkg/pty/host_mux.go c3-vis-pty
package pty

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/rpc"
	"net/url"
	"path"
	"strings"
)

type muxEntry struct {
	pat string
	// Exactly one of fn / connFn is set per entry. fn is the
	// classic net/rpc dispatch (pty / whitelist / proxy); connFn is the
	// raw-conn dispatch used by subsystems that own the wire (sftp).
	fn     handleFunc
	connFn connHandleFunc
}

type hostMux struct {
	entries []muxEntry
}

type handleFunc func(ctx context.Context, uri *url.URL, rpcS *rpc.Server) error

// connHandleFunc is the raw-conn handler shape for subsystems that
// don't use net/rpc (e.g. sftp). The mux still does request parsing,
// pattern dispatch, and writes the accept response; the handler then
// takes ownership of the conn for the rest of the session.
type connHandleFunc func(ctx context.Context, uri *url.URL, conn net.Conn) error

func (h *hostMux) Handle(pattern string, fn handleFunc) error {
	pattern = strings.TrimPrefix(pattern, "/")
	if _, err := path.Match(pattern, ""); err != nil {
		return fmt.Errorf("invalid mux pattern %q: %w", pattern, err)
	}
	h.entries = append(h.entries, muxEntry{
		pat: pattern,
		fn:  fn,
	})
	return nil
}

// HandleConn registers a raw-conn subsystem handler against pattern.
// After URI dispatch + accept-response, the conn is handed to fn for
// the remainder of the session — there is no net/rpc layer.
func (h *hostMux) HandleConn(pattern string, fn connHandleFunc) error {
	pattern = strings.TrimPrefix(pattern, "/")
	if _, err := path.Match(pattern, ""); err != nil {
		return fmt.Errorf("invalid mux pattern %q: %w", pattern, err)
	}
	h.entries = append(h.entries, muxEntry{
		pat:    pattern,
		connFn: fn,
	})
	return nil
}

func (h *hostMux) ServeConn(ctx context.Context, conn net.Conn) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		<-ctx.Done()
		_ = conn.Close() //nolint:errcheck
	}()

	uri, err := readRequest(conn)
	if err != nil {
		return writeResponse(conn, err)
	}
	for _, entry := range h.entries {
		ok, err := path.Match(entry.pat, uri.EscapedPath())
		if err != nil {
			return fmt.Errorf("path match error for pattern %q: %w", entry.pat, err)
		}
		if !ok {
			continue
		}
		if entry.connFn != nil {
			if err := writeResponse(conn, nil); err != nil {
				return err
			}
			return entry.connFn(ctx, uri, conn)
		}
		rpcS := rpc.NewServer()
		if err := entry.fn(ctx, uri, rpcS); err != nil {
			return err
		}
		if err := writeResponse(conn, nil); err != nil {
			return err
		}
		rpcS.ServeConn(conn)
		return nil
	}
	return writeResponse(conn, errors.New("invalid request"))
}
