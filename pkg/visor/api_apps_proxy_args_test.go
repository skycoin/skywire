//go:build !js

// Package visor pkg/visor/api_apps_proxy_args_test.go c3-vis-core
package visor

import (
	"testing"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// newProxyArgsVisor builds the minimum Visor that reaches the arg-rewriting
// part of StartSkysocksClient: a STDIN-pathed config (so the flush that
// follows every arg update is a no-op), a zero launcher for ResetConfig, and
// no proc manager — so the call returns ErrProcNotAvailable at the point where
// it would otherwise spawn the app, with the args already settled.
func newProxyArgsVisor(t *testing.T, args []string) *Visor {
	t.Helper()
	common, err := visorconfig.NewCommon(logging.NewMasterLogger(), visorconfig.Stdin, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &Visor{
		log:  logging.MustGetLogger("proxy_args_test"),
		conf: &visorconfig.V1{Common: common, Launcher: &visorconfig.Launcher{Apps: []appserver.AppConfig{{Name: skyenv.SkysocksClientName, Args: args}}}},
		appL: &launcher.AppLauncher{},
		tpM:  &transport.Manager{},
	}
}

// TestStartSkysocksClientKeepsOtherArgs pins the contract that made an
// operator's proxy configuration evaporate: starting the client on an exit
// REPLACED the whole args slice with a canonical six-token form, dropping
// --reconnect / --direct / --tunnels and forcing --addr back to the default —
// and persisting that loss to skywire-config.json. Only --srv may change.
func TestStartSkysocksClientKeepsOtherArgs(t *testing.T) {
	oldExit, _ := cipher.GenerateKeyPair()
	newExit, _ := cipher.GenerateKeyPair()

	v := newProxyArgsVisor(t, []string{
		"app", "skysocks-client",
		"--srv", oldExit.Hex(),
		"--addr", "127.0.0.1:1095",
		"--tunnels", "3",
		"--reconnect",
		"--direct",
	})

	if err := v.StartSkysocksClient(newExit.Hex()); err != ErrProcNotAvailable {
		t.Fatalf("StartSkysocksClient = %v, want %v", err, ErrProcNotAvailable)
	}

	got := v.conf.Launcher.Apps[0].Args
	want := []string{
		"app", "skysocks-client",
		"--srv", newExit.Hex(),
		"--addr", "127.0.0.1:1095",
		"--tunnels", "3",
		"--reconnect",
		"--direct",
	}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
	}
}

// TestStartSkysocksClientAddsAddrWhenAbsent covers the one arg the start path
// may still ADD: a client configured without --addr has to get the default
// listener, or it binds nothing the browser can reach.
func TestStartSkysocksClientAddsAddrWhenAbsent(t *testing.T) {
	exit, _ := cipher.GenerateKeyPair()
	v := newProxyArgsVisor(t, []string{"app", "skysocks-client", "--reconnect"})

	if err := v.StartSkysocksClient(exit.Hex()); err != ErrProcNotAvailable {
		t.Fatalf("StartSkysocksClient = %v, want %v", err, ErrProcNotAvailable)
	}
	args := v.conf.Launcher.Apps[0].Args
	if argValue(args, "--addr") != skyenv.SkysocksClientAddr {
		t.Fatalf("--addr = %q, want %q (args %v)", argValue(args, "--addr"), skyenv.SkysocksClientAddr, args)
	}
	if argValue(args, "--srv") != exit.Hex() {
		t.Fatalf("--srv = %q, want %q", argValue(args, "--srv"), exit.Hex())
	}
	if !argsContain(args, "--reconnect") {
		t.Fatalf("--reconnect was dropped: %v", args)
	}
}

// TestPinnedProxyExitRecorded covers the value `proxy status` reports as
// "pinned": the configured exit as it stood at boot, kept even after the app
// is re-pointed at something else.
func TestPinnedProxyExitRecorded(t *testing.T) {
	pin, _ := cipher.GenerateKeyPair()
	v := &Visor{}
	if v.PinnedProxyExit() != "" {
		t.Fatal("no pin recorded: must report empty")
	}
	key := pin.Hex()
	v.pinnedProxyExit.Store(&key)
	if v.PinnedProxyExit() != pin.Hex() {
		t.Fatalf("PinnedProxyExit = %q, want %q", v.PinnedProxyExit(), pin.Hex())
	}

	st := &appserver.AppState{AppConfig: appserver.AppConfig{Name: skyenv.SkysocksClientName}}
	v.annotatePinnedExit(st)
	if st.PinnedExit != pin.Hex() {
		t.Fatalf("AppState.PinnedExit = %q, want %q", st.PinnedExit, pin.Hex())
	}
	other := &appserver.AppState{AppConfig: appserver.AppConfig{Name: skyenv.VPNClientName}}
	v.annotatePinnedExit(other)
	if other.PinnedExit != "" {
		t.Fatal("only skysocks-client carries a pinned exit")
	}
}

// TestArgValue covers the helper that reads the pinned key out of an app's
// configured args — including the malformed trailing-flag case.
func TestArgValue(t *testing.T) {
	if got := argValue([]string{"--srv", "KEY", "--addr", ":1080"}, "--srv"); got != "KEY" {
		t.Fatalf("argValue = %q, want KEY", got)
	}
	if got := argValue([]string{"--addr", ":1080"}, "--srv"); got != "" {
		t.Fatalf("argValue = %q, want empty for a missing flag", got)
	}
	if got := argValue([]string{"--addr", ":1080", "--srv"}, "--srv"); got != "" {
		t.Fatalf("argValue = %q, want empty for a value-less trailing flag", got)
	}
}
