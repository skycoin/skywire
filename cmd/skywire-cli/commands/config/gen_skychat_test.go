// Package cliconfig cmd/skywire-cli/commands/config/gen_skychat_test.go c2-cli
//
// What skychat is launched with, pinned.
//
// These args decide whether `skywire cli skychat`, `skywire cli visor
// doctor`, the browser at 127.0.0.1:8001 and the docker e2e suite can reach
// the chat app at all, and none of those failures surface as a build error —
// they surface as "the UI is blank" a long way downstream. Nothing else in
// the repo asserts on them.
package cliconfig

import (
	"strings"
	"testing"
)

func join(args []string) string { return strings.Join(args, " ") }

// The default is a bound port. An internally-launched skychat must answer on
// the same address an externally-launched one does — the launch mode is an
// implementation detail of where the code runs, not a promise about where the
// app can be found.
func TestSkychatInternalArgsBindTheConfiguredAddrByDefault(t *testing.T) {
	got := skychatInternalArgs("127.0.0.1:8001", false, false)
	if want := "--addr 127.0.0.1:8001"; join(got) != want {
		t.Fatalf("args = %q, want %q", join(got), want)
	}

	// The docker e2e configs reach each other by container name, so the
	// all-interfaces form has to survive verbatim rather than being
	// normalized to loopback here.
	got = skychatInternalArgs("*:8001", true, false)
	if want := "--addr *:8001 --pair-enable"; join(got) != want {
		t.Fatalf("args = %q, want %q", join(got), want)
	}
}

// --portless replaces the listener; it does not merely accompany it. An
// --addr left alongside it would be a lie about where the app is.
func TestSkychatInternalArgsPortlessDropsTheAddr(t *testing.T) {
	got := skychatInternalArgs("127.0.0.1:8001", false, true)
	if want := "--portless"; join(got) != want {
		t.Fatalf("args = %q, want %q", join(got), want)
	}
	if strings.Contains(join(got), "--addr") {
		t.Fatalf("portless args still carry an address: %q", join(got))
	}

	// Pairing is orthogonal to the listener: group chat needs the pair-RPC
	// channel whether or not anything is listening on TCP.
	got = skychatInternalArgs("127.0.0.1:8001", true, true)
	if want := "--portless --pair-enable"; join(got) != want {
		t.Fatalf("args = %q, want %q", join(got), want)
	}
}

// The external form keeps its "app skychat" prefix (it is a command line, not
// launcher args) and is otherwise the same shape, which is what lets the two
// modes agree about the address.
func TestSkychatExternalArgsKeepTheAppPrefix(t *testing.T) {
	got := skychatExternalArgs("127.0.0.1:8001", true)
	if want := "app skychat --addr 127.0.0.1:8001 --pair-enable"; join(got) != want {
		t.Fatalf("args = %q, want %q", join(got), want)
	}
}
