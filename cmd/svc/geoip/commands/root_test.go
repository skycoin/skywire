// Package commands cmd/svc/geoip/commands/root_test.go: unit tests for the
// geoip root command — the JSON example helpers, the exported wrappers
// (EmbeddedGeoIP / LookupIP), lookupIP against the embedded GeoLite2 DB,
// ipFromRequest header/remote-addr precedence, the HTTP API server
// (startAPIServer driven end-to-end and shut down via SIGTERM), the command
// metadata/flags, and the CLI Run path (subprocess) plus Execute's --help.
package commands

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/geoip"
)

// ---- example helpers / exported wrappers -----------------------------------

func TestExampleJSON(t *testing.T) {
	require.Contains(t, exampleJSON(map[string]string{"k": "v"}), "\"k\"")
	require.Equal(t, "", exampleJSON(make(chan int))) // marshal error → ""
}

func TestGenerateExamples(t *testing.T) {
	out := generateExamples()
	require.Contains(t, out, "GET /?ip=8.8.8.8")
	require.Contains(t, out, "United States")
}

func TestEmbeddedGeoIP(t *testing.T) {
	require.NotEmpty(t, EmbeddedGeoIP())
}

// ---- lookupIP / LookupIP ---------------------------------------------------

func TestLookupIP(t *testing.T) {
	db, err := geoip.OpenEmbedded()
	require.NoError(t, err)
	defer func() { _ = db.Close() }() //nolint

	t.Run("valid public IP via exported wrapper", func(t *testing.T) {
		res, err := LookupIP(db, "8.8.8.8")
		require.NoError(t, err)
		require.Equal(t, "8.8.8.8", res.IP)
		require.Equal(t, "US", res.CountryCode)
	})

	t.Run("invalid IP string", func(t *testing.T) {
		_, err := lookupIP(db, "not-an-ip")
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid IP")
	})

	t.Run("private IP yields a result with no error", func(t *testing.T) {
		res, err := lookupIP(db, "127.0.0.1")
		require.NoError(t, err)
		require.Equal(t, "127.0.0.1", res.IP)
	})
}

// ---- ipFromRequest ---------------------------------------------------------

func TestIPFromRequest(t *testing.T) {
	cases := []struct {
		name       string
		realIP     string
		xff        string
		remoteAddr string
		want       string
	}{
		{"x-real-ip wins", "1.1.1.1", "2.2.2.2, 3.3.3.3", "4.4.4.4:9", "1.1.1.1"},
		{"x-forwarded-for first entry", "", "2.2.2.2, 3.3.3.3", "4.4.4.4:9", "2.2.2.2"},
		{"remote addr host:port", "", "", "4.4.4.4:9090", "4.4.4.4"},
		{"remote addr no port", "", "", "4.4.4.4", "4.4.4.4"},
		{"nothing", "", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = c.remoteAddr
			if c.realIP != "" {
				r.Header.Set("X-Real-Ip", c.realIP)
			}
			if c.xff != "" {
				r.Header.Set("X-Forwarded-For", c.xff)
			}
			require.Equal(t, c.want, ipFromRequest(r))
		})
	}
}

// ---- startAPIServer --------------------------------------------------------

// TestStartAPIServer boots the real HTTP server on a loopback port, exercises
// the handler branches, then triggers graceful shutdown via SIGTERM. Sending
// SIGTERM is safe because startAPIServer's signal.Notify (already run by the
// time the server answers requests) diverts it to the stop channel instead of
// terminating the test process.
func TestStartAPIServer(t *testing.T) {
	db, err := geoip.OpenEmbedded()
	require.NoError(t, err)

	logger := logging.MustGetLogger("geoip-test")
	const addr = "127.0.0.1:18099"
	base := "http://" + addr

	done := make(chan struct{})
	go func() {
		startAPIServer(db, addr, logger)
		close(done)
	}()

	// Wait for the listener to come up.
	var up bool
	for range 100 {
		if resp, err := http.Get(base + "/?ip=8.8.8.8"); err == nil { //nolint:gosec
			_ = resp.Body.Close() //nolint
			up = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.True(t, up, "server did not come up")

	t.Run("valid ip query → 200 JSON", func(t *testing.T) {
		resp, err := http.Get(base + "/?ip=8.8.8.8") //nolint:gosec
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }() //nolint
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var res lookupResult
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&res))
		require.Equal(t, "8.8.8.8", res.IP)
	})

	t.Run("invalid ip header → 500", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, base+"/", nil) //nolint
		req.Header.Set("X-Real-Ip", "not-an-ip")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }() //nolint
		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	})

	t.Run("no explicit ip falls back to remote addr", func(t *testing.T) {
		resp, err := http.Get(base + "/") //nolint:gosec
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()         //nolint
		require.Equal(t, http.StatusOK, resp.StatusCode) // 127.0.0.1 resolves without error
	})

	// Graceful shutdown. os.Process.Signal is portable — it compiles on Windows
	// (where this svc package is lint-typechecked but not run), and on Unix it
	// delivers SIGTERM exactly like syscall.Kill.
	proc, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)
	require.NoError(t, proc.Signal(syscall.SIGTERM))
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("server did not shut down")
	}
}

// ---- metadata / flags / Execute --------------------------------------------

func TestRootCmd_Metadata(t *testing.T) {
	require.Equal(t, "GeoIP service for skywire", RootCmd.Short)
	require.NotNil(t, RootCmd.Run)
	for _, name := range []string{"addr", "pprof", "loglvl", "tag", "db", "api"} {
		require.NotNil(t, RootCmd.Flags().Lookup(name), "flag %q should be registered", name)
	}
	require.Equal(t, ":8080", RootCmd.Flags().Lookup("addr").DefValue)
	require.Equal(t, "info", RootCmd.Flags().Lookup("loglvl").DefValue)
	require.Equal(t, "false", RootCmd.Flags().Lookup("api").DefValue)
}

func TestExecute_Help(t *testing.T) {
	defer RootCmd.SetArgs(nil)
	RootCmd.SetArgs([]string{"--help"})
	RootCmd.SetOut(os.NewFile(0, os.DevNull))
	require.NotPanics(t, Execute)
}

// TestRun_CLI drives the RootCmd.Run closure in-process on the CLI lookup path
// (embedded DB, no api mode, valid IP arg). It runs end to end — open embedded
// DB, lookup, JSON-encode to stdout — and returns normally without os.Exit.
func TestRun_CLI(t *testing.T) {
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	RootCmd.Run(RootCmd, []string{"8.8.8.8"})
	require.NoError(t, w.Close())

	out, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Contains(t, string(out), "\"ip_address\": \"8.8.8.8\"")
}
