//go:build !tinygo || (js && wasm)

package httpauthclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestClient_SharedNonceNoRace is the reward-uptime regression guard. A visor
// holds several httpauth clients to the same server as the same identity (the
// utclient uptime heartbeat and the tpdclient transport registration both hit
// the TPD). Before the shared-nonce fix, each client had its own counter, so
// concurrent same-PK requests raced the server's single monotonic nonce: both
// passed at nonce N, the server INCR'd twice while each client advanced once,
// and everything 401'd until a resync that kept losing under load — silently
// suppressing reward heartbeats for high-transport visors.
//
// With one shared counter + reqMu per (addr, PK), a process serializes its own
// requests to a server, so firing many requests through two clients
// concurrently produces zero nonce mismatches and advances the server nonce
// exactly once per request.
func TestClient_SharedNonceNoRace(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()

	var mu sync.Mutex
	expected := 1
	mismatches := 0

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/security/nonces/"+pk.Hex() {
			mu.Lock()
			n := expected
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(&NextNonceResponse{Edge: pk, NextNonce: Nonce(n)}) //nolint:errcheck,gosec
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)               //nolint:errcheck
		_ = r.Body.Close()                               //nolint:errcheck
		got, _ := strconv.Atoi(r.Header.Get("SW-Nonce")) //nolint:errcheck
		mu.Lock()
		if got == expected {
			expected++
			mu.Unlock()
			_, _ = fmt.Fprint(w, "ok") //nolint:errcheck
			return
		}
		mismatches++
		mu.Unlock()
		// Mirror the real server's invalid-nonce response so the client resyncs.
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, errorMessage) //nolint:errcheck
	}))
	defer ts.Close()

	// Two clients for the SAME (addr, PK) — the visor heartbeat + registration case.
	c1, err := NewClient(context.TODO(), ts.URL, pk, sk, &http.Client{}, "", masterLogger)
	require.NoError(t, err)
	c2, err := NewClient(context.TODO(), ts.URL, pk, sk, &http.Client{}, "", masterLogger)
	require.NoError(t, err)
	require.Same(t, c1.state, c2.state, "clients for the same (addr, PK) must share nonce state")

	const perClient = 30
	var wg sync.WaitGroup
	fire := func(c *Client) {
		defer wg.Done()
		for i := 0; i < perClient; i++ {
			req, err := http.NewRequest(http.MethodGet, ts.URL+"/foo", bytes.NewBufferString(payload))
			if err != nil {
				continue
			}
			res, err := c.Do(req)
			if err != nil {
				continue
			}
			_, _ = io.Copy(io.Discard, res.Body) //nolint:errcheck
			_ = res.Body.Close()                 //nolint:errcheck
		}
	}
	wg.Add(2)
	go fire(c1)
	go fire(c2)
	wg.Wait()

	mu.Lock()
	gotMismatches, gotExpected := mismatches, expected
	mu.Unlock()

	require.Zero(t, gotMismatches, "shared nonce state must eliminate concurrent nonce races")
	require.Equal(t, 1+2*perClient, gotExpected, "server nonce must advance exactly once per request")
}
