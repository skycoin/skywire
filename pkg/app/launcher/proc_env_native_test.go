//go:build !tinygo && !js

package launcher

import (
	"testing"

	"github.com/skycoin/skywire/pkg/app/appserver"
)

// TestProcCmdEnvInProcess: a built-in app's proc has no exec.Cmd. Restarting
// it must not dereference one.
func TestProcCmdEnvInProcess(t *testing.T) {
	if env := procCmdEnv(&appserver.Proc{}); env != nil {
		t.Fatalf("in-process proc env = %v, want nil", env)
	}
	if env := procCmdEnv(nil); env != nil {
		t.Fatalf("nil proc env = %v, want nil", env)
	}
}
