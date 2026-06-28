// Package commands dmsgcurl_test.go: unit tests for the dmsgcurl helpers
// (HTTP request building, fatal-error classification, output-file
// preparation, cancellable copy, progress reporting) plus the no-network
// early-return paths of handleRequest and the RootCmd RunE callback.
package commands

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	discmetrics "github.com/skycoin/skywire/pkg/dmsg/disc/metrics"
	discapi "github.com/skycoin/skywire/pkg/dmsg/discovery/api"
	discstore "github.com/skycoin/skywire/pkg/dmsg/discovery/store"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/logging"
)

// saveGlobals snapshots the mutable package-level vars the tests poke and
// restores them afterwards so cases don't bleed into one another.
func saveGlobals(t *testing.T) {
	t.Helper()
	out, repl, tries, wait := dmsgcurlOutput, replace, dmsgcurlTries, dmsgcurlWait
	data, lvl, px, theSK := dmsgcurlData, logLvl, proxyAddr, sk
	t.Cleanup(func() {
		dmsgcurlOutput, replace, dmsgcurlTries, dmsgcurlWait = out, repl, tries, wait
		dmsgcurlData, logLvl, proxyAddr, sk = data, lvl, px, theSK
	})
}

// ---- buildHTTPRequest ------------------------------------------------------

func TestBuildHTTPRequest(t *testing.T) {
	t.Run("GET when no data", func(t *testing.T) {
		req, err := buildHTTPRequest("http://example.com", "")
		require.NoError(t, err)
		require.Equal(t, http.MethodGet, req.Method)
	})

	t.Run("POST with body when data set", func(t *testing.T) {
		req, err := buildHTTPRequest("http://example.com", "payload")
		require.NoError(t, err)
		require.Equal(t, http.MethodPost, req.Method)
		require.Equal(t, "text/plain", req.Header.Get("Content-Type"))
		body, _ := io.ReadAll(req.Body) //nolint
		require.Equal(t, "payload", string(body))
	})

	t.Run("invalid URL errors (GET)", func(t *testing.T) {
		_, err := buildHTTPRequest("://bad", "")
		require.Error(t, err)
	})

	t.Run("invalid URL errors (POST)", func(t *testing.T) {
		_, err := buildHTTPRequest("://bad", "data")
		require.Error(t, err)
	})
}

// ---- isFatalHTTPErr --------------------------------------------------------

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return false }

var _ net.Error = timeoutErr{}

func TestIsFatalHTTPErr(t *testing.T) {
	require.True(t, isFatalHTTPErr(context.Canceled))
	require.True(t, isFatalHTTPErr(context.DeadlineExceeded))
	require.True(t, isFatalHTTPErr(timeoutErr{}))
	require.False(t, isFatalHTTPErr(errors.New("some transient error")))
}

// ---- prepareOutputFile / parseOutputFile -----------------------------------

func TestPrepareOutputFile_Stdout(t *testing.T) {
	saveGlobals(t)
	dmsgcurlOutput = ""
	f, err := prepareOutputFile()
	require.NoError(t, err)
	require.Equal(t, os.Stdout, f)
}

func TestParseOutputFile(t *testing.T) {
	dir := t.TempDir()

	t.Run("creates non-existent file (and parent dirs)", func(t *testing.T) {
		p := filepath.Join(dir, "nested", "out.bin")
		f, err := parseOutputFile(p, false)
		require.NoError(t, err)
		require.NotNil(t, f)
		_ = f.Close() //nolint
		require.FileExists(t, p)
	})

	t.Run("existing file without replace -> ErrExist", func(t *testing.T) {
		p := filepath.Join(dir, "exists.bin")
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o600))
		_, err := parseOutputFile(p, false)
		require.ErrorIs(t, err, os.ErrExist)
	})

	t.Run("existing file with replace -> truncates", func(t *testing.T) {
		p := filepath.Join(dir, "replace.bin")
		require.NoError(t, os.WriteFile(p, []byte("old content"), 0o600))
		f, err := parseOutputFile(p, true)
		require.NoError(t, err)
		require.NotNil(t, f)
		_ = f.Close() //nolint
		info, statErr := os.Stat(p)
		require.NoError(t, statErr)
		require.Equal(t, int64(0), info.Size()) // truncated
	})

	t.Run("stat error that is not IsNotExist", func(t *testing.T) {
		// A regular file used as a path component yields ENOTDIR from Stat,
		// which is an error but not os.IsNotExist -> returned as-is.
		regular := filepath.Join(dir, "regular.bin")
		require.NoError(t, os.WriteFile(regular, []byte("x"), 0o600))
		_, err := parseOutputFile(filepath.Join(regular, "child"), false)
		require.Error(t, err)
		require.False(t, os.IsNotExist(err))
	})
}

func TestPrepareOutputFile_UsesParseOutputFile(t *testing.T) {
	saveGlobals(t)
	dir := t.TempDir()
	dmsgcurlOutput = filepath.Join(dir, "p.bin")
	replace = false
	f, err := prepareOutputFile()
	require.NoError(t, err)
	require.NotNil(t, f)
	_ = f.Close() //nolint
}

// ---- closeAndCleanFile -----------------------------------------------------

func TestCloseAndCleanFile_RemovesOnError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.bin")
	f, err := os.Create(p) //nolint
	require.NoError(t, err)

	// A non-nil err triggers removal of the partial file.
	closeAndCleanFile(f, errors.New("download failed"))
	require.NoFileExists(t, p)
}

func TestCloseAndCleanFile_KeepsOnSuccess(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.bin")
	f, err := os.Create(p) //nolint
	require.NoError(t, err)

	closeAndCleanFile(f, nil)
	require.FileExists(t, p)
}

// ---- closeResponseBody -----------------------------------------------------

func TestCloseResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok")) //nolint
	}))
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL) //nolint:noctx
	require.NoError(t, err)
	require.NotPanics(t, func() { closeResponseBody(resp) })
}

// ---- cancellableCopy -------------------------------------------------------

func TestCancellableCopy_Success(t *testing.T) {
	saveGlobals(t)
	dmsgcurlOutput = "" // keep progressWriter quiet
	var dst strings.Builder
	body := io.NopCloser(strings.NewReader("hello world"))
	n, err := cancellableCopy(context.Background(), &dst, body, int64(len("hello world")))
	require.NoError(t, err)
	require.Equal(t, int64(11), n)
	require.Equal(t, "hello world", dst.String())
}

func TestCancellableCopy_Canceled(t *testing.T) {
	saveGlobals(t)
	dmsgcurlOutput = ""
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled -> first read returns the cancel error

	var dst strings.Builder
	body := io.NopCloser(strings.NewReader("data that won't be copied"))
	_, err := cancellableCopy(ctx, &dst, body, 100)
	require.Error(t, err)
}

// ---- progressWriter --------------------------------------------------------

func TestProgressWriter(t *testing.T) {
	saveGlobals(t)

	t.Run("output set, partial then complete", func(t *testing.T) {
		dmsgcurlOutput = "somefile" // enables the printf branch
		pw := &progressWriter{Total: 10}
		n, err := pw.Write(make([]byte, 4)) // current=4 < total
		require.NoError(t, err)
		require.Equal(t, 4, n)
		n, err = pw.Write(make([]byte, 6)) // current=10 == total
		require.NoError(t, err)
		require.Equal(t, 6, n)
	})

	t.Run("unknown total", func(t *testing.T) {
		dmsgcurlOutput = "somefile"
		pw := &progressWriter{Total: 0}
		n, err := pw.Write(make([]byte, 3))
		require.NoError(t, err)
		require.Equal(t, 3, n)
	})

	t.Run("output unset stays silent", func(t *testing.T) {
		dmsgcurlOutput = ""
		pw := &progressWriter{Total: 5}
		n, err := pw.Write(make([]byte, 5))
		require.NoError(t, err)
		require.Equal(t, 5, n)
	})
}

// ---- handleRequest (no-network early return) -------------------------------

func TestHandleRequest_WriteInitError(t *testing.T) {
	saveGlobals(t)
	// An existing output file with replace=false makes prepareOutputFile fail,
	// so handleRequest returns WRITE_INIT before any DMSG client work.
	dir := t.TempDir()
	out := filepath.Join(dir, "exists.bin")
	require.NoError(t, os.WriteFile(out, []byte("x"), 0o600))
	dmsgcurlOutput = out
	replace = false

	pk, theSK := cipher.GenerateKeyPair()
	u, _ := url.Parse("dmsg://" + pk.Hex() + ":80/") //nolint
	cErr := handleRequest(context.Background(), pk, theSK, &http.Client{}, u, "")
	require.Equal(t, errorCode["WRITE_INIT"], cErr.Code)
	require.Error(t, cErr.Error)
}

// ---- RootCmd.RunE (reaches handleRequest, no network) ----------------------

func runEWithExistingOutput(t *testing.T, withProxy bool) error {
	t.Helper()
	saveGlobals(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "exists.bin")
	require.NoError(t, os.WriteFile(out, []byte("x"), 0o600))
	dmsgcurlOutput = out
	replace = false
	logLvl = "fatal"
	dmsgcurlTries = 1
	if withProxy {
		proxyAddr = "127.0.0.1:1080"
	} else {
		proxyAddr = ""
	}

	pk, _ := cipher.GenerateKeyPair()
	return RootCmd.RunE(RootCmd, []string{"dmsg://" + pk.Hex() + ":80/"})
}

func TestRunE_WriteInitReturnsError(t *testing.T) {
	// handleRequest fails fast at prepareOutputFile, so RunE returns the
	// WRITE_INIT curlError rather than reaching the DMSG network.
	err := runEWithExistingOutput(t, false)
	require.Error(t, err)
}

func TestRunE_WithProxySetup(t *testing.T) {
	// Exercises the SOCKS5 proxy-setup block; the lazy dialer doesn't connect,
	// and handleRequest still short-circuits at prepareOutputFile.
	err := runEWithExistingOutput(t, true)
	require.Error(t, err)
}

// ---- handleRequest full download over an in-memory dmsg network ------------

// dmsgEnv is a minimal in-memory dmsg deployment: an HTTP discovery, one dmsg
// server, and a service dmsg client that serves HTTP on dmsg port 80. It lets
// handleRequest's UseDC path establish a real direct client and run the full
// download loop without touching the public network.
type dmsgEnv struct {
	serverEntry disc.Entry
	svcPK       cipher.PubKey
	body        string
}

func newDmsgEnv(t *testing.T, body string) dmsgEnv {
	t.Helper()
	log := logging.MustGetLogger("dmsgcurl_test")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// HTTP discovery backed by an in-memory store, in test mode.
	disco := discapi.New(log, discstore.NewMock(), discmetrics.NewEmpty(), true, false, false, "", "", 0)
	httpSrv := httptest.NewServer(disco)
	t.Cleanup(httpSrv.Close)
	dc := disc.NewHTTP(httpSrv.URL, &http.Client{}, log)

	// dmsg server on a local TCP listener, registered in the discovery.
	srvPK, srvSK := cipher.GenerateKeyPair()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := dmsg.NewServer(srvPK, srvSK, dc, &dmsg.ServerConfig{MaxSessions: 100}, nil)
	go func() { _ = srv.Serve(lis, "") }() //nolint:errcheck
	t.Cleanup(func() { _ = srv.Close() })  //nolint:errcheck
	select {
	case <-srv.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("dmsg server not ready")
	}

	// Service client connected to the discovery/server, serving HTTP on port 80.
	svcPK, svcSK := cipher.GenerateKeyPair()
	svc := dmsg.NewClient(svcPK, svcSK, dc, &dmsg.Config{MinSessions: 1})
	go svc.Serve(ctx)
	select {
	case <-svc.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("dmsg service client not ready")
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body)) //nolint
	})
	go func() { _ = dmsghttp.ListenAndServe(ctx, svcSK, handler, dc, 80, svc, log) }() //nolint:errcheck

	return dmsgEnv{
		serverEntry: disc.Entry{Static: srvPK, Server: &disc.Server{Address: lis.Addr().String(), AvailableSessions: 100}},
		svcPK:       svcPK,
		body:        body,
	}
}

func TestHandleRequest_DownloadOverDmsg(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping dmsg network integration test in -short mode")
	}
	saveGlobals(t)

	env := newDmsgEnv(t, "hello over dmsg")

	// Point the curl client's direct-client bootstrap at our in-memory server,
	// and enable UseDC so handleRequest takes the direct path (which skips the
	// discovery health-check). Restore the globals afterwards.
	origServers := dmsg.Prod.DmsgServers
	origUseDC := dmsgclient.UseDC
	origSessions := dmsgclient.DmsgSessions
	t.Cleanup(func() {
		dmsg.Prod.DmsgServers = origServers
		dmsgclient.UseDC = origUseDC
		dmsgclient.DmsgSessions = origSessions
	})
	dmsg.Prod.DmsgServers = []disc.Entry{env.serverEntry}
	dmsgclient.UseDC = true
	dmsgclient.DmsgSessions = 1

	dir := t.TempDir()
	dmsgcurlOutput = filepath.Join(dir, "downloaded.txt")
	replace = false
	dmsgcurlTries = 1
	dmsgcurlWait = 0

	pk, theSK := cipher.GenerateKeyPair()
	u, err := url.Parse("dmsg://" + env.svcPK.Hex() + ":80/")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cErr := handleRequest(ctx, pk, theSK, &http.Client{}, u, "")
	require.Equal(t, errorCode["SUCCESS"], cErr.Code)

	got, readErr := os.ReadFile(dmsgcurlOutput) //nolint
	require.NoError(t, readErr)
	require.Equal(t, env.body, string(got))
}
