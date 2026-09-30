//go:build (darwin && !ios) || linux

package netutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The kernel's answer is the same as route(8)'s (macOS) or ip(8)'s (Linux)
// wherever those can be run: what lets iOS, which cannot run them, use it.
func TestInterfaceOfDefaultRouteMatchesTheRouteTable(t *testing.T) {
	want, err := DefaultNetworkInterface()
	if err != nil || want == "" {
		t.Skipf("no default route to compare with here (%q, %v)", want, err)
	}
	got, err := interfaceOfDefaultRoute()
	require.NoError(t, err)
	require.Equal(t, want, got)
}
