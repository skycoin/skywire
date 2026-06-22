// Package commands cmd/svc/network-monitor/commands/root_test.go: unit tests
// for the network-monitor root command — the JSON example helpers,
// deregisterFromSD against an httptest server (success / error-status /
// transport-failure), the deregister subcommand's direct-signing path (single
// type, proxy normalization, all-types) driven against a stub SD server, and
// the command metadata/flags plus Execute's --help path. The root Run closure
// boots tcpproxy listeners and the deregister visor-RPC path dials a live
// visor; both end in log.Fatal and are not unit-tested.
package commands

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// ---- example helpers -------------------------------------------------------

func TestExampleJSON(t *testing.T) {
	require.Contains(t, exampleJSON(map[string]string{"k": "v"}), "\"k\"")
	require.Equal(t, "", exampleJSON(make(chan int))) // marshal error → ""
}

func TestGenerateExamples(t *testing.T) {
	out := generateExamples()
	require.Contains(t, out, "GET /health")
	require.Contains(t, out, "GET /status")
	require.Contains(t, out, "online_visors")
}

// ---- deregisterFromSD ------------------------------------------------------

func signingIdentity(t *testing.T) (cipher.PubKey, cipher.Sig) {
	t.Helper()
	pk, sk := cipher.GenerateKeyPair()
	sig, err := cipher.SignPayload([]byte(pk.Hex()), sk)
	require.NoError(t, err)
	return pk, sig
}

func TestDeregisterFromSD_Success(t *testing.T) {
	pk, sig := signingIdentity(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, "/api/services/deregister/vpn", r.URL.Path)
		require.Equal(t, pk.Hex(), r.Header.Get("NM-PK"))
		require.Equal(t, sig.Hex(), r.Header.Get("NM-Sign"))

		var body []string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, []string{"abc"}, body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	require.NoError(t, deregisterFromSD(srv.URL, "vpn", []string{"abc"}, pk, sig))
}

func TestDeregisterFromSD_ErrorStatus(t *testing.T) {
	pk, sig := signingIdentity(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()

	err := deregisterFromSD(srv.URL, "visor", []string{"abc"}, pk, sig)
	require.Error(t, err)
	require.Contains(t, err.Error(), "403")
	require.Contains(t, err.Error(), "nope")
}

func TestDeregisterFromSD_TransportError(t *testing.T) {
	pk, sig := signingIdentity(t)

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // closed → connection refused

	err := deregisterFromSD(srv.URL, "vpn", []string{"abc"}, pk, sig)
	require.Error(t, err)
	require.Contains(t, err.Error(), "request failed")
}

// ---- deregister subcommand (direct-signing path) ---------------------------

// withDeregFlags snapshots the deregister flag globals and restores them.
func withDeregFlags(t *testing.T) {
	t.Helper()
	p, ty, sd, sk, all, rpc := deregPK, deregType, deregSDURL, deregNMSK, deregAllTypes, deregRPCAddr
	t.Cleanup(func() {
		deregPK, deregType, deregSDURL, deregNMSK, deregAllTypes, deregRPCAddr = p, ty, sd, sk, all, rpc
	})
}

// stubSDServer returns an SD server that records the deregister paths it saw.
func stubSDServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &paths
}

func TestDeregisterRun_DirectSigning_SingleType(t *testing.T) {
	withDeregFlags(t)
	srv, paths := stubSDServer(t)

	pk, sk := cipher.GenerateKeyPair()
	deregPK = pk.Hex()
	deregType = "proxy" // normalized to skysocks
	deregNMSK = sk.Hex()
	deregSDURL = srv.URL
	deregAllTypes = false

	require.NotPanics(t, func() { deregisterCmd.Run(deregisterCmd, nil) })
	require.Equal(t, []string{"/api/services/deregister/skysocks"}, *paths)
}

func TestDeregisterRun_DirectSigning_AllTypes(t *testing.T) {
	withDeregFlags(t)
	srv, paths := stubSDServer(t)

	pk, sk := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	deregPK = pk.Hex() + " , " + pk2.Hex() // comma-separated + whitespace trimming
	deregType = ""
	deregNMSK = sk.Hex()
	deregSDURL = srv.URL
	deregAllTypes = true

	require.NotPanics(t, func() { deregisterCmd.Run(deregisterCmd, nil) })
	require.ElementsMatch(t, []string{
		"/api/services/deregister/vpn",
		"/api/services/deregister/visor",
		"/api/services/deregister/skysocks",
	}, *paths)
}

// ---- root Run closure ------------------------------------------------------

// withRootFlags snapshots the root command flag globals and restores them.
func withRootFlags(t *testing.T) {
	t.Helper()
	a, l, p := addr, logLvl, pprofAddr
	sd, ar, ut, tpd, dmsgd := sdURL, arURL, utURL, tpdURL, dmsgdURL
	cd, pkv, skv := cleaningDelay, pk, sk
	t.Cleanup(func() {
		addr, logLvl, pprofAddr = a, l, p
		sdURL, arURL, utURL, tpdURL, dmsgdURL = sd, ar, ut, tpd, dmsgd
		cleaningDelay, pk, sk = cd, pkv, skv
	})
}

// TestRootRun drives the root Run closure: it boots the in-memory store, the
// API and the tcpproxy listener, then triggers SignalContext's cancel by
// sending SIGTERM once the listener is up (which guarantees signal.Notify has
// already run, so the signal is diverted instead of terminating the process).
func TestRootRun(t *testing.T) {
	withRootFlags(t)
	const bind = "127.0.0.1:18091"
	addr = bind
	logLvl = "info"
	pprofAddr = ""
	cleaningDelay = 100000 // avoid a cleaning pass during the test
	// Point service URLs at an unreachable local addr so no prod traffic.
	sdURL, arURL, utURL, tpdURL, dmsgdURL = "http://127.0.0.1:1", "http://127.0.0.1:1",
		"http://127.0.0.1:1", "http://127.0.0.1:1", "http://127.0.0.1:1"

	done := make(chan struct{})
	go func() {
		RootCmd.Run(RootCmd, nil)
		close(done)
	}()

	// Wait for the tcpproxy listener; a successful dial means Run progressed
	// past SignalContext (signal.Notify registered).
	var up bool
	for range 100 {
		if c, err := net.DialTimeout("tcp", bind, 100*time.Millisecond); err == nil {
			_ = c.Close()
			up = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.True(t, up, "listener did not come up")

	require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGTERM))
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Run did not return after signal")
	}
}

// ---- metadata / flags / Execute --------------------------------------------

func TestRootCmd_Metadata(t *testing.T) {
	require.Equal(t, "Network monitor for skywire VPN and Visor.", RootCmd.Short)
	require.NotNil(t, RootCmd.Run)
	for _, name := range []string{
		"addr", "pprof", "sd-url", "ar-url", "ut-url", "tpd-url",
		"dmsgd-url", "cleaning-delay", "pk", "sk", "tag", "loglvl",
	} {
		require.NotNil(t, RootCmd.Flags().Lookup(name), "flag %q should be registered", name)
	}
	require.Equal(t, ":9080", RootCmd.Flags().Lookup("addr").DefValue)

	// deregister subcommand registered with its own flags.
	var dereg bool
	for _, c := range RootCmd.Commands() {
		if c.Use == "deregister" {
			dereg = true
		}
	}
	require.True(t, dereg, "deregister subcommand should be registered")
	require.NotNil(t, deregisterCmd.Flags().Lookup("all-types"))
}

func TestExecute_Help(t *testing.T) {
	defer RootCmd.SetArgs(nil)
	RootCmd.SetArgs([]string{"--help"})
	RootCmd.SetOut(os.NewFile(0, os.DevNull))
	require.NotPanics(t, Execute)
}
