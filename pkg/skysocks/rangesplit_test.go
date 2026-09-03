package skysocks

import (
	"bufio"
	"bytes"
	"net/http"
	"strings"
	"testing"
)

func TestInjectRange(t *testing.T) {
	head := []byte("GET /f HTTP/1.1\r\nHost: h\r\nUser-Agent: x\r\n\r\n")
	got := injectRange(head, 0, 4095)
	want := "GET /f HTTP/1.1\r\nHost: h\r\nUser-Agent: x\r\nRange: bytes=0-4095\r\n\r\n"
	if string(got) != want {
		t.Fatalf("injectRange:\n got %q\nwant %q", got, want)
	}
	// A head without the CRLFCRLF terminator is returned untouched.
	bad := []byte("GET /f HTTP/1.1\r\nHost: h\r\n")
	if string(injectRange(bad, 0, 1)) != string(bad) {
		t.Fatal("injectRange should no-op on a head without terminator")
	}
}

func TestParseContentRangeTotal(t *testing.T) {
	cases := map[string]struct {
		want int64
		ok   bool
	}{
		"bytes 0-4095/87858592": {87858592, true},
		"bytes 100-199/200":     {200, true},
		"bytes 0-4095/*":        {0, false},
		"":                      {0, false},
		"garbage":               {0, false},
	}
	for in, exp := range cases {
		got, ok := parseContentRangeTotal(in)
		if ok != exp.ok || (ok && got != exp.want) {
			t.Errorf("parseContentRangeTotal(%q) = (%d,%v), want (%d,%v)", in, got, ok, exp.want, exp.ok)
		}
	}
}

func TestStatusCode(t *testing.T) {
	if statusCode("HTTP/1.1 206 Partial Content\r\n") != 206 {
		t.Fatal("206 not parsed")
	}
	if statusCode("HTTP/1.1 200 OK\r\n") != 200 {
		t.Fatal("200 not parsed")
	}
	if statusCode("garbage") != 0 {
		t.Fatal("garbage should be 0")
	}
}

func TestSplittableRequest(t *testing.T) {
	mk := func(raw string) *http.Request {
		r, err := http.ReadRequest(bufio.NewReader(strings.NewReader(raw)))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		return r
	}
	if !splittableRequest(mk("GET /f HTTP/1.1\r\nHost: h\r\n\r\n")) {
		t.Fatal("plain GET should be splittable")
	}
	if splittableRequest(mk("POST /f HTTP/1.1\r\nHost: h\r\n\r\n")) {
		t.Fatal("POST should not be splittable")
	}
	if splittableRequest(mk("GET /f HTTP/1.1\r\nHost: h\r\nRange: bytes=0-9\r\n\r\n")) {
		t.Fatal("already-ranged GET should not be splittable")
	}
	if splittableRequest(mk("GET /f HTTP/1.1\r\nHost: h\r\nUpgrade: websocket\r\n\r\n")) {
		t.Fatal("upgrade should not be splittable")
	}
}

func TestBuildRangedGet(t *testing.T) {
	req, err := http.ReadRequest(bufio.NewReader(strings.NewReader(
		"GET /path?q=1 HTTP/1.1\r\nHost: ftp.gnu.org\r\nUser-Agent: curl/8\r\nCookie: a=b\r\nRange: bytes=0-9\r\nConnection: keep-alive\r\n\r\n")))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := string(buildRangedGet(req, "ftp.gnu.org", "\"etag123\"", 4096, 8191))
	// Original headers preserved except hop-by-hop / range; our headers appended.
	for _, must := range []string{
		"GET /path?q=1 HTTP/1.1\r\n",
		"Host: ftp.gnu.org\r\n",
		"User-Agent: curl/8\r\n",
		"Cookie: a=b\r\n",
		"Range: bytes=4096-8191\r\n",
		"If-Range: \"etag123\"\r\n",
		"Connection: close\r\n",
	} {
		if !strings.Contains(got, must) {
			t.Errorf("buildRangedGet missing %q in:\n%s", must, got)
		}
	}
	// The client's original Range (0-9) and keep-alive must NOT survive.
	if strings.Contains(got, "bytes=0-9") {
		t.Error("original Range header leaked")
	}
	if strings.Contains(got, "keep-alive") {
		t.Error("original Connection header leaked")
	}
	if !strings.HasSuffix(got, "\r\n\r\n") {
		t.Error("request not terminated with blank line")
	}
}

func TestReadSocks5Reply(t *testing.T) {
	// IPv4 reply: VER REP RSV ATYP=1 + 4 addr + 2 port = 10 bytes.
	ipv4 := []byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
	got, err := readSocks5Reply(bytes.NewReader(ipv4))
	if err != nil || !bytes.Equal(got, ipv4) {
		t.Fatalf("ipv4 reply: got %v err %v", got, err)
	}
	// Domain reply: VER REP RSV ATYP=3 LEN=3 "abc" + 2 port.
	dom := []byte{0x05, 0x00, 0x00, 0x03, 0x03, 'a', 'b', 'c', 0, 0}
	got, err = readSocks5Reply(bytes.NewReader(dom))
	if err != nil || !bytes.Equal(got, dom) {
		t.Fatalf("domain reply: got %v err %v", got, err)
	}
	// IPv6 reply: 4 header + 16 addr + 2 port = 22 bytes.
	v6 := make([]byte, 22)
	v6[0], v6[3] = 0x05, 0x04
	got, err = readSocks5Reply(bytes.NewReader(v6))
	if err != nil || len(got) != 22 {
		t.Fatalf("ipv6 reply: got len %d err %v", len(got), err)
	}
}

func TestClassifyHTTP(t *testing.T) {
	cases := []struct {
		in              string
		decided, isHTTP bool
	}{
		{"", false, false},              // empty → keep reading
		{"GE", false, false},            // prefix of GET → keep reading
		{"GET ", true, true},            // full method token → HTTP
		{"GET /x HTTP/1.1", true, true}, // HTTP
		{"POST ", true, true},           // HTTP
		{"ping", true, false},           // not a method → not HTTP, decided now
		{"\x05\x01\x00", true, false},   // raw SOCKS bytes → not HTTP
		{"GET/", true, false},           // malformed (no space) → not HTTP
	}
	for _, c := range cases {
		d, h := classifyHTTP([]byte(c.in))
		if d != c.decided || h != c.isHTTP {
			t.Errorf("classifyHTTP(%q) = (%v,%v), want (%v,%v)", c.in, d, h, c.decided, c.isHTTP)
		}
	}
}

func TestIfRangeValidator(t *testing.T) {
	h := make(map[string][]string)
	// Strong ETag preferred.
	h["Etag"] = []string{"\"abc\""}
	h["Last-Modified"] = []string{"Wed, 21 Oct 2015 07:28:00 GMT"}
	if got := ifRangeValidator(h); got != "\"abc\"" {
		t.Fatalf("want strong etag, got %q", got)
	}
	// Weak ETag rejected → fall back to Last-Modified.
	h["Etag"] = []string{"W/\"weak\""}
	if got := ifRangeValidator(h); got != "Wed, 21 Oct 2015 07:28:00 GMT" {
		t.Fatalf("weak etag should fall back to last-modified, got %q", got)
	}
}

func TestNumChunks(t *testing.T) {
	if numChunks(10, 4) != 3 {
		t.Fatal("10/4 should be 3 chunks")
	}
	if numChunks(8, 4) != 2 {
		t.Fatal("8/4 should be 2 chunks")
	}
	if numChunks(1, 4) != 1 {
		t.Fatal("1/4 should be 1 chunk")
	}
}
