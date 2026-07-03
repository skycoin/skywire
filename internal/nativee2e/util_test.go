//go:build client_e2e
// +build client_e2e

package nativee2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// httpGet fetches a URL with a short timeout, returning the body on 2xx.
func httpGet(url string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close() //nolint:errcheck
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return string(b), fmt.Errorf("http %d", resp.StatusCode)
	}
	return string(b), nil
}

// visorPK returns the visor's public key via RPC (66-hex).
func visorPK(t *testing.T, rpc string) string {
	t.Helper()
	out, err := cli("visor", "--rpc", rpc, "pk")
	if err != nil {
		t.Fatalf("get pk (%s): %v (%s)", rpc, err, out)
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if len(line) >= 66 {
			cand := line[len(line)-66:]
			if is66Hex(cand) {
				return cand
			}
		}
	}
	t.Fatalf("no PK in output for %s: %q", rpc, out)
	return ""
}

func is66Hex(s string) bool {
	if len(s) != 66 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
