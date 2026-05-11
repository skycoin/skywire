package skynetweb

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skynetca"
)

const testPK = "027087fe40d97f7f0be4a0dc768462ddbb371d4b9e7679d4f11f117d757b9856ed"

func TestRun_RejectsTLSMITMWithoutMinter(t *testing.T) {
	err := Run(context.Background(), nil, fakeDialer{}, Config{
		ProxyPort: 19999,
		TLSMITM:   true,
	})
	if err == nil || !strings.Contains(err.Error(), "LeafMinter") {
		t.Errorf("expected LeafMinter error, got %v", err)
	}
}

// TestSOCKS5_MITMHandshakeAndSplice exercises the full path:
// SOCKS5 server → Dial callback → fake skynet "transport" → TLS
// termination via skynetca → plaintext splice → back to the
// (real) TLS client. Confirms a browser-style handshake against
// the local CA succeeds and that bytes flow through.
func TestSOCKS5_MITMHandshakeAndSplice(t *testing.T) {
	ca, caKey, err := skynetca.GenerateCA(skynetca.CAOptions{})
	if err != nil {
		t.Fatal(err)
	}
	minter := skynetca.NewMinter(ca, caKey, skynetca.LeafOptions{})
	proxyPort := pickFreePort(t)

	dialer := fakeDialer{
		onDial: func() (net.Conn, error) {
			client, server := net.Pipe()
			go func() {
				defer server.Close()                                        //nolint:errcheck,gosec
				_ = server.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck,gosec
				buf := make([]byte, 4096)
				_, _ = server.Read(buf)                                                                               //nolint:errcheck,gosec
				_, _ = server.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 5\r\nConnection: close\r\n\r\nhello")) //nolint:errcheck,gosec
			}()
			return client, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = Run(ctx, logging.MustGetLogger("skynetweb-test"), dialer, Config{ //nolint:errcheck,gosec
			ProxyPort:  proxyPort,
			TLSMITM:    true,
			TLSPort:    443,
			LeafMinter: minter,
		})
	}()

	if err := waitForListener("127.0.0.1", proxyPort, 2*time.Second); err != nil {
		t.Fatal(err)
	}

	socksDialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", proxyPort), nil, proxy.Direct)
	if err != nil {
		t.Fatal(err)
	}
	host := testPK + ".skynet"
	conn, err := socksDialer.Dial("tcp", host+":443")
	if err != nil {
		t.Fatalf("SOCKS5 dial: %v", err)
	}
	defer conn.Close() //nolint:errcheck,gosec

	pool := x509.NewCertPool()
	pool.AddCert(ca)
	tlsConn := tls.Client(conn, &tls.Config{ServerName: host, RootCAs: pool})
	if err := tlsConn.Handshake(); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	if _, err := tlsConn.Write([]byte("GET / HTTP/1.1\r\nHost: " + host + "\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	body, _ := io.ReadAll(tlsConn) //nolint:errcheck,gosec
	if !strings.Contains(string(body), "hello") {
		t.Errorf("body = %q, want to contain hello", string(body))
	}
}

// TestSOCKS5_NonTLSPortStillSplices verifies that an HTTP (port
// 80) skynet target is unaffected by TLSMITM mode — it still goes
// through pure splice.
func TestSOCKS5_NonTLSPortStillSplices(t *testing.T) {
	ca, caKey, _ := skynetca.GenerateCA(skynetca.CAOptions{}) //nolint:errcheck,gosec
	minter := skynetca.NewMinter(ca, caKey, skynetca.LeafOptions{})
	proxyPort := pickFreePort(t)

	dialer := fakeDialer{
		onDial: func() (net.Conn, error) {
			client, server := net.Pipe()
			go func() {
				defer server.Close()                                        //nolint:errcheck,gosec
				_ = server.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck,gosec
				buf := make([]byte, 4096)
				_, _ = server.Read(buf)                                                                                //nolint:errcheck,gosec
				_, _ = server.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 6\r\nConnection: close\r\n\r\nplain!")) //nolint:errcheck,gosec
			}()
			return client, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = Run(ctx, logging.MustGetLogger("skynetweb-test"), dialer, Config{ //nolint:errcheck,gosec
			ProxyPort:  proxyPort,
			TLSMITM:    true,
			TLSPort:    443,
			LeafMinter: minter,
		})
	}()
	if err := waitForListener("127.0.0.1", proxyPort, 2*time.Second); err != nil {
		t.Fatal(err)
	}

	socksDialer, _ := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", proxyPort), nil, proxy.Direct) //nolint:errcheck,gosec
	host := testPK + ".skynet"
	conn, err := socksDialer.Dial("tcp", host+":80")
	if err != nil {
		t.Fatalf("SOCKS5 dial: %v", err)
	}
	defer conn.Close() //nolint:errcheck,gosec
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: " + host + "\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	body, _ := io.ReadAll(conn) //nolint:errcheck,gosec
	if !strings.Contains(string(body), "plain!") {
		t.Errorf("plain port body = %q", string(body))
	}
}

type fakeDialer struct {
	onDial func() (net.Conn, error)
}

func (f fakeDialer) DialSkynet(_ context.Context, _ cipher.PubKey, _ uint16) (net.Conn, error) {
	if f.onDial == nil {
		return nil, errors.New("fakeDialer: no onDial set")
	}
	return f.onDial()
}

func pickFreePort(t *testing.T) uint {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := uint(lis.Addr().(*net.TCPAddr).Port) //nolint:gosec // ephemeral port always fits in uint
	_ = lis.Close()                              //nolint:errcheck,gosec
	return port
}

func waitForListener(host string, port uint, max time.Duration) error {
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			_ = c.Close() //nolint:errcheck,gosec
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("listener at %s not up within %s", addr, max)
}
