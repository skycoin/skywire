package cliskychat

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
)

// A file offered on an offline peer's inbox is reported as such, not as a
// failure.
func TestSendFileOffered(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"f1","parked":true}`)) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	path := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	prevAddr, prevTransport := httpAddr, chatTransport
	chatTransport = &visorTransport{next: http.DefaultTransport}
	t.Cleanup(func() {
		httpAddr, chatTransport = prevAddr, prevTransport
		RootCmd.PersistentFlags().Lookup("addr").Changed = false
	})
	pk, _ := cipher.GenerateKeyPair()
	var out, errb bytes.Buffer
	addr := strings.TrimPrefix(srv.URL, "http://")
	code := cmdutil.Run(context.Background(), RootCmd, []string{"--addr", addr, "send-file", "--to", pk.Hex(), path}, nil, &out, &errb)
	if code != 0 || !strings.Contains(out.String(), pk.Hex()+" is offline. The file was offered") {
		t.Fatalf("code %d, out %q, err %q", code, out.String(), errb.String())
	}
}
