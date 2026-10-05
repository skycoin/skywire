package cliskychat

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

type fakeVisor struct {
	visorapi.API
	got visorapi.SkychatHTTPIn
}

func (f *fakeVisor) SkychatHTTP(in visorapi.SkychatHTTPIn) (visorapi.SkychatHTTPOut, error) {
	f.got = in
	return visorapi.SkychatHTTPOut{Status: 200, Headers: map[string][]string{"Content-Type": {"application/json"}}, Body: []byte(`{"ok":true}`)}, nil
}

// TestPortlessSkychatGoesThroughTheVisor: with nothing listening at --addr,
// a request for it is answered by the visor's SkychatHTTP, and the live
// streams fail with a clear message instead of hanging.
func TestPortlessSkychatGoesThroughTheVisor(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close() //nolint:errcheck

	prevAddr, prevClient := httpAddr, visorClient
	t.Cleanup(func() { httpAddr, visorClient = prevAddr, prevClient })
	httpAddr = addr
	fv := &fakeVisor{}
	visorClient = func() (visorapi.API, error) { return fv, nil }
	c := &http.Client{Transport: &visorTransport{next: http.DefaultTransport}}

	resp, err := c.Post("http://"+addr+"/message?x=1", "application/json", strings.NewReader(`{"m":1}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body) //nolint:errcheck
	_ = resp.Body.Close()            //nolint:errcheck
	if resp.StatusCode != 200 || string(body) != `{"ok":true}` {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}
	if fv.got.Method != "POST" || fv.got.Path != "message" || fv.got.Query != "x=1" || string(fv.got.Body) != `{"m":1}` {
		t.Fatalf("visor got %+v", fv.got)
	}

	if _, err := c.Get("http://" + addr + "/sse"); err == nil || !strings.Contains(err.Error(), "live streams") {
		t.Fatalf("a stream should fail with a clear message, got %v", err)
	}
}
