// Package commands cmd/svc/transport-setup/commands/root_test.go: unit tests
// for the transport-setup command tree — the JSON example helpers, the command
// metadata/flags wired up in init(), the add/rm/list subcommand happy paths
// (driven against an httptest server), and Execute's help path. The RootCmd.Run
// closure boots a dmsg-serving node and is not unit-testable.
package commands

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestMain swaps http.DefaultClient for one that never pools connections. The
// add/rm/list subcommands route HTTP through bitfield/script, which uses
// http.DefaultClient; with the default pooling transport, a connection to an
// already-closed prior test server gets reused under repeated (-count) runs and
// fails with "connection refused". DisableKeepAlives forces a fresh dial every
// request, making the subcommand tests deterministic.
func TestMain(m *testing.M) {
	orig := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	code := m.Run()
	http.DefaultClient = orig
	os.Exit(code)
}

func TestExampleJSON(t *testing.T) {
	out := exampleJSON(map[string]string{"k": "v"})
	require.Contains(t, out, "\"k\"")

	// Unmarshalable value (channel) → json.MarshalIndent fails → "".
	require.Equal(t, "", exampleJSON(make(chan int)))
}

func TestGenerateExamples(t *testing.T) {
	out := generateExamples()
	require.NotEmpty(t, out)
	require.Contains(t, out, "Request/Response Examples:")
	require.Contains(t, out, "POST /add")
	require.Contains(t, out, "POST /remove")
	require.Contains(t, out, "GET /{pk}/transports")
}

func TestRootCmd_Metadata(t *testing.T) {
	require.Equal(t, "Transport setup server for skywire", RootCmd.Short)
	require.NotNil(t, RootCmd.Run)
	require.NotNil(t, RootCmd.Flags().Lookup("config"))
	require.NotNil(t, RootCmd.Flags().Lookup("loglvl"))

	// add/rm/list subcommands registered in init().
	names := map[string]bool{}
	for _, c := range RootCmd.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["add"])
	require.True(t, names["rm"])
	require.True(t, names["list"])
}

func TestExecute_Help(t *testing.T) {
	defer RootCmd.SetArgs(nil)
	RootCmd.SetArgs([]string{"--help"})
	RootCmd.SetOut(os.NewFile(0, os.DevNull))
	RootCmd.SetErr(os.NewFile(0, os.DevNull))
	require.NotPanics(t, Execute)
}

// newServer starts an httptest server hardened against the bitfield/script
// package's use of the shared http.DefaultClient/DefaultTransport, which pools
// keep-alive connections. Across repeated (-count) runs a pooled connection to
// an already-closed prior test server could be reused, surfacing as a flaky
// "connection refused". We disable server keep-alives AND evict the default
// transport's idle connections before the test runs and after the server closes
// so no stale connection ever survives between tests.
func newServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	closeIdle()
	srv := httptest.NewUnstartedServer(h)
	srv.Config.SetKeepAlivesEnabled(false)
	srv.Start()
	t.Cleanup(func() {
		srv.Close()
		closeIdle()
	})
	return srv
}

func closeIdle() {
	if tr, ok := http.DefaultTransport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
}

// silenceStdout points os.Stdout at /dev/null for the duration of the test so
// the subcommands' fmt.Printf output doesn't pollute test logs. A stable file
// (not an os.Pipe) is used deliberately — pipe churn races with the HTTP
// transport's file descriptors under repeated (-count) runs.
func silenceStdout(t *testing.T) {
	t.Helper()
	orig := os.Stdout
	devnull, err := os.Open(os.DevNull)
	require.NoError(t, err)
	os.Stdout = devnull
	t.Cleanup(func() {
		os.Stdout = orig
		_ = devnull.Close()
	})
}

// restoreGlobals snapshots the subcommand flag globals and returns a restore.
func restoreGlobals() func() {
	sFrom, sTo, sID, sType, sAddr, sNice := fromPK, toPK, tpID, tpType, tpsnAddr, nice
	return func() {
		fromPK, toPK, tpID, tpType, tpsnAddr, nice = sFrom, sTo, sID, sType, sAddr, sNice
	}
}

func TestAddTPCmd_Run(t *testing.T) {
	defer restoreGlobals()()

	var hit bool
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		require.Equal(t, "/add", r.URL.Path)
		hit = true
		_, _ = io.WriteString(w, `{"id":"e7a7f1b3-c040-47f8-9e12-a0a1459b3456","type":"stcpr"}`)
	})

	from, _ := cipher.GenerateKeyPair()
	to, _ := cipher.GenerateKeyPair()
	fromPK = from.Hex()
	toPK = to.Hex()
	tpType = "stcpr"
	tpsnAddr = srv.URL
	silenceStdout(t)

	nice = false
	require.NotPanics(t, func() { addTPCmd.Run(addTPCmd, nil) })
	require.True(t, hit)

	// Pretty-print branch.
	nice = true
	require.NotPanics(t, func() { addTPCmd.Run(addTPCmd, nil) })
}

func TestRmTPCmd_Run(t *testing.T) {
	defer restoreGlobals()()

	var hit bool
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		require.Equal(t, "/remove", r.URL.Path)
		hit = true
		_, _ = io.WriteString(w, `{"success":true}`)
	})

	from, _ := cipher.GenerateKeyPair()
	fromPK = from.Hex()
	tpID = uuid.New().String()
	tpsnAddr = srv.URL
	nice = false
	silenceStdout(t)

	require.NotPanics(t, func() { rmTPCmd.Run(rmTPCmd, nil) })
	require.True(t, hit)
}

func TestListTPCmd_Run(t *testing.T) {
	defer restoreGlobals()()

	from, _ := cipher.GenerateKeyPair()
	var hit bool
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		require.Equal(t, "/"+from.Hex()+"/transports", r.URL.Path)
		hit = true
		_, _ = io.WriteString(w, `[{"id":"e7a7f1b3-c040-47f8-9e12-a0a1459b3456","type":"stcpr"}]`)
	})

	fromPK = from.Hex()
	tpsnAddr = srv.URL
	nice = false
	silenceStdout(t)

	require.NotPanics(t, func() { listTPCmd.Run(listTPCmd, nil) })
	require.True(t, hit)
}
