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

// TestParseFreshIsARestart parses as three runs would. A run keeps nothing from
// the one before, a repeated slice flag replaces its default instead of adding
// to it, and an unset one gets its default back.
func TestParseFreshIsARestart(t *testing.T) {
	fs := skycoinweb.RootCmd.Flags()
	strs := func(name string) []string {
		v, err := fs.GetStringArray(name)
		require.NoError(t, err)
		return v
	}
	require.NoError(t, parseFresh(fs, []string{"--wallet-dir", "/a", "--node-url", "http://x", "--no-listen"}))
	require.Equal(t, []string{"http://x"}, strs("node-url"))

	require.NoError(t, parseFresh(fs, []string{"--wallet-dir", "/b", "--node-url", "http://y"}))
	require.Equal(t, []string{"/b"}, strs("wallet-dir"))
	require.Equal(t, []string{"http://y"}, strs("node-url"))
	noListen, err := fs.GetBool("no-listen")
	require.NoError(t, err)
	require.False(t, noListen)

	require.NoError(t, parseFresh(fs, nil))
	require.Equal(t, []string{"https://node.skycoin.com"}, strs("node-url"))
	require.Empty(t, strs("wallet-dir"))
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

// A restart's old run stops after the new run has published. Its Mount(nil)
// must leave the new handler in place.
func TestPublisherKeepsSuccessor(t *testing.T) {
	oldRun, newRun := publisher(), publisher()
	oldRun(http.NotFoundHandler())
	newRun(http.NotFoundHandler())
	current := launcher.GetHTTPHandler(skyenv.SkycoinWebName)

	oldRun(nil)
	require.Same(t, current, launcher.GetHTTPHandler(skyenv.SkycoinWebName))
	newRun(nil)
	require.Nil(t, launcher.GetHTTPHandler(skyenv.SkycoinWebName))
}
