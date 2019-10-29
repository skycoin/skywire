package ptyutil

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/SkycoinProject/skywire-mainnet/pkg/util/pathutil"
)

// MakeSignalCtx makes a signal context.
func MakeSignalCtx() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())

	ch := make(chan os.Signal)
	signal.Notify(ch, []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT}...)

	go func() {
		select {
		case sig := <-ch:
			log.Printf("Received signal %v: closing...", sig)
			cancel()
		case <-ctx.Done():
			return
		}
	}()

	return ctx, cancel
}

func SignalDial(network, addr string, fn func(conn net.Conn)) {
	ctx, cancel := MakeSignalCtx()
	defer cancel()

	conn, err := net.Dial(network, addr)
	if err != nil {
		log.Fatalf("failed to dial to dmsgexec-server: %v", err)
	}

	go func() {
		<-ctx.Done()
		_ = conn.Close() //nolint:errcheck
	}()

	fn(conn)
}

func SignalDialE(network, addr string, fn func(conn net.Conn) error) error {
	ctx, cancel := MakeSignalCtx()
	defer cancel()

	conn, err := net.Dial(network, addr)
	if err != nil {
		return fmt.Errorf("failed to dial to local server: %v", err)
	}

	go func() {
		<-ctx.Done()
		_ = conn.Close() //nolint:errcheck
	}()

	return fn(conn)
}

const (
	ConfDir = ".dmsgpty"
)

func DefaultKeysPath() string { return filepath.Join(pathutil.HomeDir(), ConfDir, "keys.json") }
func DefaultAuthPath() string { return filepath.Join(pathutil.HomeDir(), ConfDir, "whitelist.json") }
