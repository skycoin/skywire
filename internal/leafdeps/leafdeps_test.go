// Package leafdeps keeps the small utility packages small. A help screen or a
// signal handler must not link the dmsg client through one convenience import.
package leafdeps

import (
	"os/exec"
	"strings"
	"testing"
)

const module = "github.com/skycoin/skywire/"

// leaves are packages that other code imports for one small thing. Each must
// stay clear of every prefix in heavy.
var leaves = []string{
	"pkg/buildinfo",
	"pkg/cliout",
	"pkg/cliout/clihelp",
	"pkg/cmdutil",
	"pkg/flags",
	"pkg/httputil",
	"pkg/logging",
	"pkg/netutil",
	"pkg/skyenv",
	"pkg/skywireconfig/skyenvfile",
	"pkg/util/deadline",
	"pkg/util/osutil",
	"pkg/util/pathutil",
	"pkg/util/rpcutil",
}

// heavy are the networking stack and the subsystems built on it.
var heavy = []string{
	module + "pkg/app",
	module + "pkg/dmsg",
	module + "pkg/router",
	module + "pkg/services",
	module + "pkg/transport",
	module + "pkg/visor",
	"github.com/0magnet/yamux",
	"github.com/coder/websocket",
	"github.com/pion/",
	"github.com/quic-go/",
	"github.com/skycoin/skycoin/",
	"github.com/xtaci/smux",
}

func TestLeafPackagesStayLight(t *testing.T) {
	for _, leaf := range leaves {
		out, err := exec.Command("go", "list", "-deps", module+leaf).Output() //nolint:gosec // leaf is from the list above
		if err != nil {
			t.Fatalf("go list %s: %v", leaf, err)
		}
		var bad []string
		for _, dep := range strings.Fields(string(out)) {
			for _, h := range heavy {
				if strings.HasPrefix(dep, h) {
					bad = append(bad, dep)
					break
				}
			}
		}
		if len(bad) > 0 {
			t.Errorf("%s reaches %d heavy packages, first %s. Find the import with go mod why, then move what it uses down.",
				leaf, len(bad), bad[0])
		}
	}
}
