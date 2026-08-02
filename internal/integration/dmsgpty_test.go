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
//
// CONFIG DEPENDENCY: #3658 gated the RPC-initiated exec path behind
// pty.allow_rpc_exec (OFF by default), so all three e2e visor configs set it —
// including visor-a, which is only ever the CALLER here (the negative case must
// reach the remote's whitelist to be rejected BY IT, not die locally on the
// gate). Without the flag every exec below fails with "RPC-initiated exec is
// disabled" and this test times out.
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

// retryPtyExec polls exec until want accepts its result, returning the last
// result and whether it ever passed.
//
// Why not require.Eventually: its message args are evaluated at CALL time, so a
// failure printed the ZERO ExecResult — the previous CI failure here reported
// `exit=0 stdout="<nil>"` for a run whose every attempt had actually failed with
// "dmsgpty: RPC-initiated exec is disabled", and the real cause had to be dug
// out of the source. Logging each attempt as it happens keeps the reason in the
// CI log where the next person will look.
func retryPtyExec(t *testing.T, timeout, interval time.Duration,
	exec func() (ExecResult, error), want func(ExecResult, error) bool) (ExecResult, bool) {
	t.Helper()

	var res ExecResult
	var err error
	deadline := time.Now().Add(timeout)
	for attempt := 1; ; attempt++ {
		res, err = exec()
		if want(res, err) {
			return res, true
		}
		t.Logf("pty exec attempt %d: err=%v exit=%d stdout=%q stderr=%q",
			attempt, err, res.ExitCode, strings.TrimSpace(res.Stdout()), strings.TrimSpace(res.Stderr()))
		if time.Now().After(deadline) {
			return res, false
		}
		time.Sleep(interval)
	}
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
		res, ok := retryPtyExec(t, 60*time.Second, 3*time.Second, func() (ExecResult, error) {
			return env.VisorPtyExec(visorB, pk, "/bin/hostname")
		}, func(res ExecResult, err error) bool {
			return err == nil && res.ExitCode == 0 && strings.TrimSpace(res.Stdout()) == remote
		})
		require.True(t, ok, "dmsgpty exec visor-b->%s did not return the remote hostname", remote)
		t.Logf("dmsgpty exec visor-b->%s: hostname=%q exit=%d", remote, strings.TrimSpace(res.Stdout()), res.ExitCode)
	}

	// Negative: visor-a is not visor-c's hypervisor and is not on visor-c's pty
	// whitelist, so a pty exec visor-a->visor-c must be rejected by the remote
	// dmsgpty host (non-zero exit + a "rejected" message). Retry past any cold-
	// session transient until the deterministic rejection surfaces.
	neg, ok := retryPtyExec(t, 60*time.Second, 3*time.Second, func() (ExecResult, error) {
		return env.VisorPtyExec(visorA, pkC, "/bin/hostname")
	}, func(res ExecResult, err error) bool {
		return err == nil && res.ExitCode != 0 && strings.Contains(res.Combined(), "reject")
	})
	require.True(t, ok, "expected non-whitelisted pty exec visor-a->visor-c to be rejected")
	t.Logf("dmsgpty whitelist gate held: visor-a->visor-c rejected (exit=%d)", neg.ExitCode)

	t.Logf("TestEnv_DmsgPtyExec completed in %v", time.Since(start).Round(time.Second))
}
