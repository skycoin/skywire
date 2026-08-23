package proxyinterstitial

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPageTransient(t *testing.T) {
	p := Page("magnetosphere.net", "", "skysocks", false)
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
	p := Page("example.dmsg", "no route to host", "dmsg", true)
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
	p := Page("<script>evil()</script>", "", "skysocks", false)
	if strings.Contains(p, "<script>evil()") {
		t.Error("target host was not HTML-escaped")
	}
}

func TestPageStatusFooter(t *testing.T) {
	// A concrete mechanism links to its reserved status host.
	for _, mech := range []string{"skysocks", "dmsg", "skynet"} {
		p := Page("host."+mech, "", mech, false)
		if !strings.Contains(p, "http://status."+mech+"/") {
			t.Errorf("mechanism %q: page missing status-host link", mech)
		}
	}
	// The generic fallback has no dedicated surface, so no status link.
	if p := Page("host", "", "", false); strings.Contains(p, "http://status.") {
		t.Error("generic mechanism should not link a status host")
	}
}

func TestConnServesHTTP(t *testing.T) {
	c := Conn("host.skynet", "", "skynet", false)
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
	body, _ := io.ReadAll(resp.Body) //nolint:errcheck
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
	go func() { done <- ServeSOCKS5(srv, "", "skysocks", nil); srv.Close() }() //nolint:errcheck,gosec

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
	// Drain the whole response: the page embeds the branded cloud image as a
	// base64 data: URI, so the "Building a route" heading falls well past a
	// single read and net.Pipe writes block until fully consumed.
	var got bytes.Buffer
	buf := make([]byte, 4096)
	for {
		n, err := cli.Read(buf)
		got.Write(buf[:n])
		if err != nil {
			break
		}
	}
	if !strings.Contains(got.String(), "Building a route") {
		t.Errorf("did not receive interstitial; got %q", got.String())
	}
	if err := <-done; err != nil {
		t.Errorf("ServeSOCKS5: %v", err)
	}
}

func TestServeSOCKS5_HTTPSDeclined(t *testing.T) {
	cli, srv := net.Pipe()
	defer cli.Close() //nolint:errcheck
	done := make(chan error, 1)
	go func() { done <- ServeSOCKS5(srv, "", "skysocks", nil); srv.Close() }() //nolint:errcheck,gosec

	_ = cli.SetDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck
	cli.Write([]byte{0x05, 0x01, 0x00})                  //nolint:errcheck,gosec
	io.ReadFull(cli, make([]byte, 2))                    //nolint:errcheck,gosec
	// CONNECT 1.2.3.4:443 (IPv4 atyp)
	cli.Write([]byte{0x05, 0x01, 0x00, 0x01, 1, 2, 3, 4, 0x01, 0xBB}) //nolint:errcheck,gosec
	if err := <-done; err == nil {
		t.Error("expected ServeSOCKS5 to decline the 443 request")
	}
}

// serveSOCKS5CONNECT drives the client half of a SOCKS5 no-auth CONNECT to the
// given domain:port over cli and returns the full response body the server
// wrote back (the CONNECT reply is consumed here). Shared by the override tests.
func serveSOCKS5CONNECT(t *testing.T, cli net.Conn, host string, port uint16) string {
	t.Helper()
	_ = cli.SetDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck
	if _, err := cli.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	sel := make([]byte, 2)
	if _, err := io.ReadFull(cli, sel); err != nil {
		t.Fatal(err)
	}
	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))}
	req = append(req, []byte(host)...)
	req = append(req, byte(port>>8), byte(port))
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
	if _, err := cli.Write([]byte("GET / HTTP/1.1\r\nHost: " + host + "\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	buf := make([]byte, 4096)
	for {
		n, err := cli.Read(buf)
		got.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return got.String()
}

// TestServeSOCKS5_StatusOverride verifies that when a statusOverride is supplied
// (as skysocks-client does with its status.skysocks page), a CONNECT to the
// reserved host is answered with the override body — not the interstitial —
// exactly when the exit is down, while a normal host still gets the interstitial.
func TestServeSOCKS5_StatusOverride(t *testing.T) {
	const statusHost = "status.skysocks"
	const statusBody = "HTTP/1.1 200 OK\r\nContent-Length: 11\r\n\r\nSTATUS-PAGE"
	override := func(host string) []byte {
		if host == statusHost {
			return []byte(statusBody)
		}
		return nil
	}

	t.Run("reserved host serves override", func(t *testing.T) {
		cli, srv := net.Pipe()
		defer cli.Close() //nolint:errcheck
		done := make(chan error, 1)
		go func() { done <- ServeSOCKS5(srv, "", "skysocks", override); srv.Close() }() //nolint:errcheck,gosec
		got := serveSOCKS5CONNECT(t, cli, statusHost, 80)
		if !strings.Contains(got, "STATUS-PAGE") {
			t.Errorf("status host did not receive override; got %q", got)
		}
		if strings.Contains(got, "Building a route") {
			t.Errorf("status host was shadowed by the interstitial; got %q", got)
		}
		if err := <-done; err != nil {
			t.Errorf("ServeSOCKS5: %v", err)
		}
	})

	t.Run("normal host still serves interstitial", func(t *testing.T) {
		cli, srv := net.Pipe()
		defer cli.Close() //nolint:errcheck
		done := make(chan error, 1)
		go func() { done <- ServeSOCKS5(srv, "", "skysocks", override); srv.Close() }() //nolint:errcheck,gosec
		got := serveSOCKS5CONNECT(t, cli, "example.com", 80)
		if !strings.Contains(got, "Building a route") {
			t.Errorf("normal host did not receive interstitial; got %q", got)
		}
		if strings.Contains(got, "STATUS-PAGE") {
			t.Errorf("normal host wrongly got the override; got %q", got)
		}
		if err := <-done; err != nil {
			t.Errorf("ServeSOCKS5: %v", err)
		}
	})
}

// TestDumpSamples writes the two page variants to the scratchpad for a manual
// visual check when RUN_DUMP is set; skipped in normal CI.
func TestDumpSamples(t *testing.T) {
	dir := os.Getenv("INTERSTITIAL_DUMP_DIR")
	if dir == "" {
		t.Skip("set INTERSTITIAL_DUMP_DIR to dump sample HTML")
	}
	_ = os.WriteFile(dir+"/interstitial_transient.html", []byte(Page("magnetosphere.net", "", "skysocks", false)), 0644)   //nolint:errcheck,gosec
	_ = os.WriteFile(dir+"/interstitial_error.html", []byte(Page("skycoin.dmsg", "no route to host", "dmsg", true)), 0644) //nolint:errcheck,gosec
}

// TestPageMechanismAndSteps pins the interstitial-copy fixes: the fetch step
// names the concrete mechanism (skysocks/dmsg/skynet, not a generic "over
// skywire"), the misleading "Finding a working exit" / redundant "Reaching your
// visor" steps are gone, and the branded Skywire cloud mark is present.
func TestPageMechanismAndSteps(t *testing.T) {
	for mech, want := range map[string]string{
		"skysocks": "Fetching the page over skysocks",
		"dmsg":     "Fetching the page over dmsg",
		"skynet":   "Fetching the page over skynet",
		"":         "Fetching the page over skywire", // unknown → generic
	} {
		p := Page("host.example", "", mech, false)
		if !strings.Contains(p, want) {
			t.Errorf("mechanism %q: page missing %q", mech, want)
		}
	}

	p := Page("host.example", "", "skysocks", false)
	for _, gone := range []string{"Finding a working exit", "Reaching your skywire visor"} {
		if strings.Contains(p, gone) {
			t.Errorf("page still contains stale step %q", gone)
		}
	}
	for _, want := range []string{"Building a route across the mesh", `alt="skywire" src="data:image/png;base64,`} { // the embedded Skycoin cloud brand mark
		if !strings.Contains(p, want) {
			t.Errorf("page missing %q", want)
		}
	}
}
