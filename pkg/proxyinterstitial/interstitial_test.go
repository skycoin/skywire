package proxyinterstitial

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPageTransient(t *testing.T) {
	p := Page("magnetosphere.net", "", false)
	for _, want := range []string{"<!doctype html>", "http-equiv=\"refresh\"", "skywire", "magnetosphere.net", "Building a route"} {
		if !strings.Contains(p, want) {
			t.Errorf("transient page missing %q", want)
		}
	}
	if strings.Contains(p, "Retry") {
		t.Error("transient page should not offer a manual retry")
	}
}

func TestPageError(t *testing.T) {
	p := Page("example.dmsg", "no route to host", true)
	if strings.Contains(p, "http-equiv=\"refresh\"") {
		t.Error("error page must not auto-refresh")
	}
	for _, want := range []string{"Retry", "no route to host", "example.dmsg"} {
		if !strings.Contains(p, want) {
			t.Errorf("error page missing %q", want)
		}
	}
}

func TestPageEscapesTarget(t *testing.T) {
	p := Page("<script>evil()</script>", "", false)
	if strings.Contains(p, "<script>evil()") {
		t.Error("target host was not HTML-escaped")
	}
}

func TestConnServesHTTP(t *testing.T) {
	c := Conn("host.skynet", "", false)
	// Write (the browser's request) is discarded but must not error.
	if _, err := c.Write([]byte("GET / HTTP/1.1\r\nHost: host.skynet\r\n\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Building a route") {
		t.Error("body is not the interstitial page")
	}
	if _, ok := c.RemoteAddr().(*net.TCPAddr); !ok {
		t.Error("RemoteAddr must be *net.TCPAddr for go-socks5")
	}
}

func TestShouldServe(t *testing.T) {
	for _, p := range []string{"", "80", "8080"} {
		if !ShouldServe(p) {
			t.Errorf("ShouldServe(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"443", "22", "1080"} {
		if ShouldServe(p) {
			t.Errorf("ShouldServe(%q) = true, want false", p)
		}
	}
}

func TestServeSOCKS5_HTTPPort(t *testing.T) {
	cli, srv := net.Pipe()
	defer cli.Close() //nolint:errcheck
	done := make(chan error, 1)
	go func() { done <- ServeSOCKS5(srv, ""); srv.Close() }() //nolint:errcheck

	_ = cli.SetDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck
	// greeting: VER=5, 1 method, no-auth
	if _, err := cli.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	sel := make([]byte, 2)
	if _, err := io.ReadFull(cli, sel); err != nil {
		t.Fatal(err)
	}
	if sel[0] != 0x05 || sel[1] != 0x00 {
		t.Fatalf("method select = %v", sel)
	}
	// CONNECT domain "a.b" :80
	req := []byte{0x05, 0x01, 0x00, 0x03, 0x03, 'a', '.', 'b', 0x00, 0x50}
	if _, err := cli.Write(req); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(cli, reply); err != nil {
		t.Fatal(err)
	}
	if reply[0] != 0x05 || reply[1] != 0x00 {
		t.Fatalf("connect reply = %v", reply)
	}
	// Now send the HTTP request; expect the interstitial back.
	if _, err := cli.Write([]byte("GET / HTTP/1.1\r\nHost: a.b\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	n, _ := cli.Read(buf)
	if !strings.Contains(string(buf[:n]), "Building a route") {
		t.Errorf("did not receive interstitial; got %q", string(buf[:n]))
	}
	if err := <-done; err != nil {
		t.Errorf("ServeSOCKS5: %v", err)
	}
}

func TestServeSOCKS5_HTTPSDeclined(t *testing.T) {
	cli, srv := net.Pipe()
	defer cli.Close() //nolint:errcheck
	done := make(chan error, 1)
	go func() { done <- ServeSOCKS5(srv, ""); srv.Close() }() //nolint:errcheck

	_ = cli.SetDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck
	cli.Write([]byte{0x05, 0x01, 0x00})                  //nolint:errcheck
	io.ReadFull(cli, make([]byte, 2))                    //nolint:errcheck
	// CONNECT 1.2.3.4:443 (IPv4 atyp)
	cli.Write([]byte{0x05, 0x01, 0x00, 0x01, 1, 2, 3, 4, 0x01, 0xBB}) //nolint:errcheck
	if err := <-done; err == nil {
		t.Error("expected ServeSOCKS5 to decline the 443 request")
	}
}

// TestDumpSamples writes the two page variants to the scratchpad for a manual
// visual check when RUN_DUMP is set; skipped in normal CI.
func TestDumpSamples(t *testing.T) {
	dir := os.Getenv("INTERSTITIAL_DUMP_DIR")
	if dir == "" {
		t.Skip("set INTERSTITIAL_DUMP_DIR to dump sample HTML")
	}
	_ = os.WriteFile(dir+"/interstitial_transient.html", []byte(Page("magnetosphere.net", "", false)), 0644)       //nolint:errcheck,gosec
	_ = os.WriteFile(dir+"/interstitial_error.html", []byte(Page("skycoin.dmsg", "no route to host", true)), 0644) //nolint:errcheck,gosec
}
