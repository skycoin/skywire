//go:build !no_ci
// +build !no_ci

// Package integration_test — pkg/../internal/integration/dmsgpty_test.go: end-to-end
// coverage for dmsgpty (remote command execution / pseudo-terminal over dmsg).
//
// dmsgpty lets one visor run commands on another over the dmsg overlay (dmsg port
// 22), gated by the target's peer whitelist. This closes the "dmsgpty: no e2e" gap
// from the coverage report by driving the production path — `skywire cli pty exec
// <pk> -- <cmd>` → visor RPC DmsgPtyExec → Host.ExecRemote → dmsg stream to the
// remote dmsgpty host → command runs there, stdout/exit-code captured back.
//
// The test runner container is visor-b, which is the HYPERVISOR of visor-a and
// visor-c (see the e2e visor configs' `hypervisors`), so visor-b's PK is
// auto-whitelisted for pty on both leaves (newPeerWhitelist seeds own PK +
// hypervisors) — no explicit whitelist step needed. We prove:
//   - remote exec works and actually runs on the REMOTE host (assert the remote's
//     own /bin/hostname), and
//   - the whitelist gate denies a non-whitelisted caller (visor-a → visor-c).
//
// dmsgpty rides dmsg directly (no skywire transport), so no `tp add` is required —
// only live dmsg sessions.
package integration_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// VisorPtyExec runs a one-shot command on remotePK's dmsgpty host using
// callerVisor's RPC as the caller identity (`skywire cli pty exec`). It returns
// the raw ExecResult — ExitCode mirrors the remote command (or is non-zero on a
// whitelist rejection / RPC failure); err is only set on a docker-exec failure.
// Command tokens must be space-free (the exec harness splits on spaces).
func (env *TestEnv) VisorPtyExec(callerVisor, remotePK string, cmdAndArgs ...string) (ExecResult, error) {
	full := fmt.Sprintf("/release/skywire cli --rpc %s:3435 pty exec %s -- %s",
		callerVisor, remotePK, strings.Join(cmdAndArgs, " "))
	return env.execResult(full)
}

// TestEnv_DmsgPtyExec runs /bin/hostname on visor-a and visor-c over dmsgpty from
// the hypervisor visor-b and asserts the output is the remote's hostname (proving
// remote execution), then asserts a non-whitelisted caller is rejected.
func TestEnv_DmsgPtyExec(t *testing.T) {
	start := time.Now()
	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB, visorC})

	// dmsgpty rides the dmsg overlay, so the peers just need live dmsg sessions.
	for _, visor := range []string{visorA, visorB, visorC} {
		err := env.WaitForDmsgDiscoveryEntry(visor, 120*time.Second)
		if err != nil {
			t.Logf("Failed to find DMSG discovery entry for %s: %v", visor, err)
			if logs, logErr := env.ReadLog(visor); logErr == nil {
				t.Logf("Logs for %s:\n%s", visor, logs)
			}
		}
		require.NoError(t, err, "Visor %s not found in DMSG discovery", visor)
	}

	pkC := env.visorPKs[visorC]

	// Positive: exec /bin/hostname on each remote leaf visor and require the
	// output to be that visor's hostname — if the command had run locally on
	// visor-b it would print "visor-b". Retry to absorb a cold dmsg session (the
	// first DialStream to a peer can return the transient dmsg-202).
	for _, remote := range []string{visorA, visorC} {
		pk := env.visorPKs[remote]
		var res ExecResult
		var err error
		require.Eventually(t, func() bool {
			res, err = env.VisorPtyExec(visorB, pk, "/bin/hostname")
			return err == nil && res.ExitCode == 0 && strings.TrimSpace(res.Stdout()) == remote
		}, 60*time.Second, 3*time.Second,
			"dmsgpty exec visor-b->%s did not return the remote hostname (last err=%v exit=%d stdout=%q stderr=%q)",
			remote, err, res.ExitCode, strings.TrimSpace(res.Stdout()), strings.TrimSpace(res.Stderr()))
		t.Logf("dmsgpty exec visor-b->%s: hostname=%q exit=%d", remote, strings.TrimSpace(res.Stdout()), res.ExitCode)
	}

	// Negative: visor-a is not visor-c's hypervisor and is not on visor-c's pty
	// whitelist, so a pty exec visor-a->visor-c must be rejected by the remote
	// dmsgpty host (non-zero exit + a "rejected" message). Retry past any cold-
	// session transient until the deterministic rejection surfaces.
	var neg ExecResult
	var negErr error
	require.Eventually(t, func() bool {
		neg, negErr = env.VisorPtyExec(visorA, pkC, "/bin/hostname")
		return negErr == nil && neg.ExitCode != 0 && strings.Contains(neg.Combined(), "reject")
	}, 60*time.Second, 3*time.Second,
		"expected non-whitelisted pty exec visor-a->visor-c to be rejected (last err=%v exit=%d out=%q)",
		negErr, neg.ExitCode, strings.TrimSpace(neg.Combined()))
	t.Logf("dmsgpty whitelist gate held: visor-a->visor-c rejected (exit=%d)", neg.ExitCode)

	t.Logf("TestEnv_DmsgPtyExec completed in %v", time.Since(start).Round(time.Second))
}
