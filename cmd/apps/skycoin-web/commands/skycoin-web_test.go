// Package commands cmd/apps/skycoin-web/commands/skycoin-web_test.go c4-app-wallet
package commands

import (
	"context"
	"net/http"
	"testing"
	"time"

	skycoinweb "github.com/skycoin/skycoin/cmd/skycoin-web/commands"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// TestResetFlagsRestoresDefaults is a restart: the second run must not keep the
// first run's wallet dir, and must get the default node back.
func TestResetFlagsRestoresDefaults(t *testing.T) {
	fs := skycoinweb.RootCmd.Flags()
	require.NoError(t, fs.Parse([]string{"--wallet-dir", "/a", "--node-url", "http://x", "--no-listen"}))
	resetFlags(fs)
	require.NoError(t, fs.Parse([]string{"--wallet-dir", "/b"}))

	dirs, err := fs.GetStringArray("wallet-dir")
	require.NoError(t, err)
	require.Equal(t, []string{"/b"}, dirs)
	nodes, err := fs.GetStringArray("node-url")
	require.NoError(t, err)
	require.Equal(t, []string{"https://node.skycoin.com"}, nodes)
	noListen, err := fs.GetBool("no-listen")
	require.NoError(t, err)
	require.False(t, noListen)
}

// TestRunSkycoinWebRunsOnlyTheWallet guards the in-process start: executing the
// cobra tree ran its root with os.Args, which in a visor was the visor itself.
func TestRunSkycoinWebRunsOnlyTheWallet(t *testing.T) {
	RootCmd.Run = func(*cobra.Command, []string) { t.Fatal("the command tree's root ran") }
	t.Cleanup(func() { RootCmd.Run = nil })

	ctx, cancel := context.WithCancel(context.Background())
	published := make(chan http.Handler, 1)
	go func() {
		for ctx.Err() == nil {
			if h := launcher.GetHTTPHandler(skyenv.SkycoinWebName); h != nil {
				published <- h
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	err := runWallet(ctx, []string{"skycoin", "web", "--no-listen", "--node-url", "http://127.0.0.1:1", "--wallet-dir", t.TempDir()})
	require.NoError(t, err)
	require.NotNil(t, <-published)
	require.Nil(t, launcher.GetHTTPHandler(skyenv.SkycoinWebName), "a stopped wallet leaves the registry")
}
