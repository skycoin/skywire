// Package cligot got_http_test.go: exercises the dl / req / head command Run
// closures over a local httptest server (the HTTP path, no visor needed) and
// the skywire-scheme funcs' no-visor error paths.
package cligot

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// resetGotGlobals zeroes every package-level flag var so each command test
// starts from a clean slate (the cobra Run closures read these directly).
func resetGotGlobals(t *testing.T) {
	t.Helper()
	output, dir, headers, data = "", "", nil, ""
	proxyAddr, userAgent = "", ""
	concurrency, chunkSize = 0, 0
	resume, verbose = false, false
	t.Cleanup(func() {
		output, dir, headers, data = "", "", nil, ""
		proxyAddr, userAgent = "", ""
		concurrency, chunkSize = 0, 0
		resume, verbose = false, false
	})
}

// contentServer serves a fixed body via http.ServeContent (range + HEAD aware).
func contentServer(t *testing.T) (*httptest.Server, []byte) {
	t.Helper()
	body := bytes.Repeat([]byte("a"), 8192)
	modTime := time.Unix(1, 0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "file.bin", modTime, bytes.NewReader(body))
	}))
	t.Cleanup(srv.Close)
	return srv, body
}

// captureStdout redirects os.Stdout for the duration of fn and returns what
// was written (keeps command body/headers out of the test log).
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	_ = w.Close()
	os.Stdout = orig
	return <-done
}

func TestDlCmd_HTTP(t *testing.T) {
	srv, body := contentServer(t)
	resetGotGlobals(t)
	dir = t.TempDir()
	concurrency = 1

	cmd := &cobra.Command{}
	dlCmd.Run(cmd, []string{srv.URL + "/file.bin"})

	got, err := os.ReadFile(filepath.Join(dir, "file.bin"))
	require.NoError(t, err)
	require.Equal(t, body, got)
}

func TestRootCmd_DefaultsToDownload(t *testing.T) {
	srv, body := contentServer(t)
	resetGotGlobals(t)
	dir = t.TempDir()
	concurrency = 1

	cmd := &cobra.Command{}
	// RootCmd.Run delegates to dlCmd.Run.
	RootCmd.Run(cmd, []string{srv.URL + "/file.bin"})

	got, err := os.ReadFile(filepath.Join(dir, "file.bin"))
	require.NoError(t, err)
	require.Equal(t, body, got)
}

func TestReqCmd_HTTP(t *testing.T) {
	srv, body := contentServer(t)
	resetGotGlobals(t)
	verbose = true // also exercise the header-dump branch

	cmd := &cobra.Command{}
	out := captureStdout(t, func() {
		reqCmd.Run(cmd, []string{"GET", srv.URL + "/file.bin"})
	})
	require.Equal(t, string(body), out)
}

func TestReqCmd_HTTP_ToFile(t *testing.T) {
	srv, body := contentServer(t)
	resetGotGlobals(t)
	dir = t.TempDir()
	output = filepath.Join(dir, "resp.out")

	cmd := &cobra.Command{}
	reqCmd.Run(cmd, []string{"GET", srv.URL + "/file.bin"})

	got, err := os.ReadFile(output)
	require.NoError(t, err)
	require.Equal(t, body, got)
}

func TestHeadCmd_HTTP(t *testing.T) {
	srv, _ := contentServer(t)
	resetGotGlobals(t)

	cmd := &cobra.Command{}
	out := captureStdout(t, func() {
		headCmd.Run(cmd, []string{srv.URL + "/file.bin"})
	})
	require.Contains(t, out, "200")
}

// ---- skywire-scheme funcs without a visor (RPC dial fails) -----------------

func TestSkywireFuncs_NoVisor(t *testing.T) {
	resetGotGlobals(t)
	pk, _ := cipher.GenerateKeyPair()
	url := "skynet://" + pk.Hex() + ":80/health"
	cmd := &cobra.Command{}

	// Each parses the URL successfully, then fails at the RPC dial because
	// no visor is running — exercising parse + header build + the
	// requestSkywire client-error path.
	require.Error(t, downloadSkywire(cmd, url))
	require.Error(t, requestSkywireCmd(cmd, "GET", url))
	require.Error(t, headSkywire(cmd, url))

	// dmsg scheme routes through the same path.
	require.Error(t, headSkywire(cmd, "dmsg://"+pk.Hex()+"/"))

	// Parse errors propagate before any RPC attempt.
	require.Error(t, downloadSkywire(cmd, "skynet://not-a-pk/"))
	require.Error(t, requestSkywireCmd(cmd, "GET", "skynet://not-a-pk/"))
	require.Error(t, headSkywire(cmd, "skynet://not-a-pk/"))
}

func TestRequestSkywireCmd_WithBody_NoVisor(t *testing.T) {
	resetGotGlobals(t)
	data = "payload"
	pk, _ := cipher.GenerateKeyPair()
	cmd := &cobra.Command{}
	// data != "" exercises the body-reader branch before the RPC failure.
	require.Error(t, requestSkywireCmd(cmd, "POST", "skynet://"+pk.Hex()+"/api"))
}
