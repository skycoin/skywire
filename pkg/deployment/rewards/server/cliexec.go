// Package rewardsserver pkg/deployment/rewards/server/cliexec.go c3-app-rewards
package rewardsserver

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/bitfield/script"
)

// skycoinCLI is a pipe whose commands reach --skycoin-node through RPC_ADDR
// in their own environment. Setting it on the process would change it for
// everything else in a visor that embeds the reward service.
func skycoinCLI() *script.Pipe {
	p := script.NewPipe()
	if skycoinNode != "" {
		p = p.WithEnv(append(os.Environ(), "RPC_ADDR="+skycoinNode))
	}
	return p
}

// logBackupsDir is the survey directory of the work dir, absolute so a
// command finds it whatever the working directory of the process.
func logBackupsDir() string {
	dir := filepath.Join(wd, "log_backups")
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

// runSkywireCLI runs `skywire cli <args>` and returns its stdout and stderr.
// A variable so a test can see the arguments.
var runSkywireCLI = func(args ...string) ([]byte, error) {
	return exec.Command("skywire", append([]string{"cli"}, args...)...).CombinedOutput() //nolint:gosec
}
