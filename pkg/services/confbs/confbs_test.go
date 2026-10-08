package confbs

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services"
)

// The block serves the bootstrap config from its file over plain HTTP.
func TestRunServesConfig(t *testing.T) {
	f, ok := services.Lookup(Type)
	require.True(t, ok)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()
	require.NoError(t, lis.Close())
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"stun_servers":["192.0.2.1:3478"]}`), 0o600))

	raw, err := json.Marshal(map[string]string{"type": Type, "mode": "http", "addr": addr, "config_path": path})
	require.NoError(t, err)
	svc, err := f(raw, logging.MustGetLogger("test"))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()

	var body struct {
		Stun []string `json:"stun_servers"`
	}
	require.Eventually(t, func() bool {
		resp, err := http.Get("http://" + addr + "/") //nolint:noctx
		if err != nil {
			return false
		}
		defer resp.Body.Close() //nolint:errcheck
		return json.NewDecoder(resp.Body).Decode(&body) == nil
	}, 5*time.Second, 20*time.Millisecond)
	require.Equal(t, []string{"192.0.2.1:3478"}, body.Stun)

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
