package skymailbridge

import (
	"context"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"testing"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
)

// pipeDialer connects every dial to a function playing the peer.
type pipeDialer struct{ serve func(net.Conn) }

func (d pipeDialer) Dial(_ context.Context, _ cipher.PubKey, _ uint16) (net.Conn, error) {
	c, s := net.Pipe()
	go d.serve(s)
	return c, nil
}

// recorder is a Handler that keeps what it is given.
type recorder struct {
	mu  sync.Mutex
	got []Envelope
}

func (r *recorder) Rcpt(net.Conn, string, string, []string) error { return nil }
func (r *recorder) Deliver(_ context.Context, env Envelope) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, env)
	return "stored", nil
}

// TestBridgeRelaysThroughTheSharedSessionLoop drives the bridge the way
// Postfix does and checks the peer receives the rewritten envelope and
// the body byte for byte, dot-stuffed lines included.
func TestBridgeRelaysThroughTheSharedSessionLoop(t *testing.T) {
	peer := &recorder{}
	dialer := pipeDialer{serve: func(c net.Conn) {
		defer c.Close() //nolint:errcheck
		handleSession(context.Background(), c, peer, "peer")
	}}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, lis, dialer, Config{}, logrus.New()) }()

	pk := mustPK(t)
	rcpt := "user@magnetosphere.net." + pk.DNSLabel() + ".skynet"
	body := "Subject: t\r\n\r\n.dot line\r\nplain\r\n"
	if err := smtp.SendMail(lis.Addr().String(), nil, "", []string{rcpt}, []byte(body)); err != nil {
		t.Fatalf("send via bridge: %v", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Serve: %v", err)
	}

	if len(peer.got) != 1 {
		t.Fatalf("peer got %d envelopes, want 1", len(peer.got))
	}
	env := peer.got[0]
	if env.From != "" {
		t.Errorf("null reverse path became %q", env.From)
	}
	if len(env.Rcpts) != 1 || env.Rcpts[0] != "user@magnetosphere.net" {
		t.Errorf("rcpts = %v, want the mode-b stripped form", env.Rcpts)
	}
	if string(env.Body) != body {
		t.Errorf("body = %q, want %q", env.Body, body)
	}
}

func TestBridgeRefusesNonSkynetRecipients(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Serve(ctx, lis, pipeDialer{serve: func(c net.Conn) { _ = c.Close() }}, Config{}, logrus.New()) //nolint:errcheck

	err = smtp.SendMail(lis.Addr().String(), nil, "a@b.c", []string{"x@example.com"}, []byte("x\r\n"))
	if err == nil || !strings.Contains(err.Error(), "550") {
		t.Fatalf("err = %v, want a 550", err)
	}
}

// TestBridgeDialsTheNetworkTheSuffixNames: with a dialer per suffix,
// .skynet mail goes out on the skynet dialer and .dmsg mail on the dmsg
// one, never the other; an envelope mixing them is deferred.
func TestBridgeDialsTheNetworkTheSuffixNames(t *testing.T) {
	var mu sync.Mutex
	used := map[string]int{}
	peer := &recorder{}
	network := func(name string) Dialer {
		return pipeDialer{serve: func(c net.Conn) {
			mu.Lock()
			used[name]++
			mu.Unlock()
			defer c.Close() //nolint:errcheck
			handleSession(context.Background(), c, peer, "peer")
		}}
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Serve(ctx, lis, nil, Config{Dialers: map[string]Dialer{".skynet": network("skynet"), ".dmsg": network("dmsg")}}, logrus.New()) //nolint:errcheck

	pk := mustPK(t)
	for _, sfx := range []string{".skynet", ".dmsg"} {
		if err := smtp.SendMail(lis.Addr().String(), nil, "a@b.c", []string{"u@" + pk.DNSLabel() + sfx}, []byte("x\r\n")); err != nil {
			t.Fatalf("%s: %v", sfx, err)
		}
	}
	mu.Lock()
	if used["skynet"] != 1 || used["dmsg"] != 1 {
		t.Fatalf("dials per network = %v, want one each", used)
	}
	mu.Unlock()

	err = smtp.SendMail(lis.Addr().String(), nil, "a@b.c",
		[]string{"u@" + pk.DNSLabel() + ".skynet", "v@" + pk.DNSLabel() + ".dmsg"}, []byte("x\r\n"))
	if err == nil || !strings.Contains(err.Error(), "451") {
		t.Fatalf("mixed networks: err = %v, want a 451", err)
	}
}
