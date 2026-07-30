// Package main scripts/wasm-headless/localdmsg/main.go c3-vis-wasm
//
// Minimal self-contained dmsg server for the HEADLESS wasm-visor smoke test
// (Tier B): one unified TCP+WS dmsg server on loopback with an in-memory
// discovery, so the real compiled wasm-visor blob — running under Node, no
// browser — has a ws:// seed to bootstrap against. Prints machine-readable
// SEEDPK= / SEEDWS= lines followed by READY, then serves until stdin closes
// (so the driving script's exit reaps it deterministically).
package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

func main() {
	dc := disc.NewMock(0)
	pk, sk := cipher.GenerateKeyPair()

	const maxSessions = 32
	srv := dmsg.NewServer(pk, sk, dc, &dmsg.ServerConfig{MaxSessions: maxSessions, UpdateInterval: 0}, nil)
	srv.SetLogger(logging.MustGetLogger("localdmsg"))

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)
		os.Exit(1)
	}
	tcpAddr := lis.Addr().String()
	wsURL := "ws://" + tcpAddr + "/dmsg"

	entry := disc.NewServerEntry(pk, 0, tcpAddr, maxSessions)
	entry.Server.AddressWS = wsURL
	if err := entry.Sign(sk); err != nil {
		fmt.Fprintln(os.Stderr, "sign:", err)
		os.Exit(1)
	}
	if err := dc.PostEntry(context.Background(), entry); err != nil {
		fmt.Fprintln(os.Stderr, "post entry:", err)
		os.Exit(1)
	}

	go func() {
		if err := srv.ServeWithWS(lis, tcpAddr, wsURL); err != nil {
			fmt.Fprintln(os.Stderr, "serve:", err)
		}
	}()
	<-srv.Ready()

	fmt.Printf("SEEDPK=%s\n", pk.Hex())
	fmt.Printf("SEEDWS=%s\n", wsURL)
	fmt.Println("READY")

	// Serve until the parent closes our stdin (or EOF) — deterministic reaping.
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
	}
	_ = srv.Close() //nolint:errcheck
}
