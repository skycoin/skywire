// Package skysocksc cmd/skywire-cli/commands/proxy/testargs_test.go c4-vis-cli
package skysocksc

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

type argsRecorder struct {
	visorapi.API
	app   string
	args  []string
	calls []string
}

func (a *argsRecorder) StopApp(string) error  { a.calls = append(a.calls, "stop"); return nil }
func (a *argsRecorder) StartApp(string) error { a.calls = append(a.calls, "start"); return nil }

func (a *argsRecorder) SetAppArgs(app string, args []string) error {
	a.app, a.args = app, args
	return nil
}

// The restore writes the operator's args back exactly, in order. Rebuilding
// them through a map lost the order, so the "app <name>" pair and a flag's
// value could land anywhere and the client then refused to start.
func TestRestoreKeepsArgsInOrder(t *testing.T) {
	args := []string{
		"--srv", "02a49bc0aa1b5b78f638e9189be4ed095bac5d6839c828465a8350f80ac07629c0",
		"--standby-pool", "1", "--reconnect", "--tunnels", "2", "--addr", ":1080",
	}
	rec := &argsRecorder{}
	r := &proxyArgsRestorer{rpc: rec, appName: "skysocks-client", args: args, running: true}
	r.restore()
	assert.Equal(t, "skysocks-client", rec.app)
	assert.Equal(t, args, rec.args)
	assert.Equal(t, []string{"stop", "start"}, rec.calls, "a client that ran before the test runs again on its own args")
}
