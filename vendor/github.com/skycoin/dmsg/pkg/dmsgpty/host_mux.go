package dmsgpty

import (
	"context"
	"errors"
	"net"
	"net/rpc"
	"net/url"
	"path"
	"strings"
)

type muxEntry struct {
	pat string
	fn  handleFunc
}

type hostMux struct {
	entries []muxEntry
}

type handleFunc func(ctx context.Context, uri *url.URL, rpcS *rpc.Server) error

func (h *hostMux) Handle(pattern string, fn handleFunc) {
	pattern = strings.TrimPrefix(pattern, "/")
	if _, err := path.Match(pattern, ""); err != nil {
		panic(err)
	}
	h.entries = append(h.entries, muxEntry{
		pat: pattern,
		fn:  fn,
	})
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
			panic(err)
		}
		if !ok {
			continue
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
