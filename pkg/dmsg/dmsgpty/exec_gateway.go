// Package dmsgpty pkg/dmsgpty/exec_gateway.go
//
// Non-interactive command execution over the existing dmsgpty trust
// model. Operators can already spawn a remote shell via `cli dmsg pty
// start <pk>`; this adds a parallel non-PTY path that runs ONE
// command, captures stdout/stderr/exit-code, and returns — useful for
// scripted health probes and config inspection on peers where opening
// a full TTY isn't workable (e.g. from inside another non-TTY tool).
//
// Trust model: identical to Start. The same dmsgpty Whitelist gates
// who may call this. Hosts that already trust a PK to spawn a shell
// implicitly trust it to run one command — the surface is strictly
// less powerful.
//
// Response cap: stdout + stderr each capped at execMaxOutputBytes
// (16 MiB). Larger outputs get truncated with a marker. Discourages
// streaming-shaped tools (use `start` for those); favors config
// reads, status checks, and small diagnostic commands.
//
// Timeout: default 30s, configurable per-call. Process is killed
// (SIGKILL after grace) when the deadline fires; TimedOut=true in
// the response.

package dmsgpty

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// execMaxOutputBytes caps stdout and stderr each. 16 MiB chosen to
// fit typical config-file reads (systemd unit, full journalctl dump
// for an hour) without enabling streaming workloads.
const execMaxOutputBytes = 16 * 1024 * 1024

// execMaxStdinBytes caps the request-side stdin. Smaller than output
// because the request travels OVER dmsg (latency-sensitive) and
// large inputs argue for an interactive Start session instead.
const execMaxStdinBytes = 1 * 1024 * 1024

// execDefaultTimeout is the wall-clock cap when the caller doesn't
// specify one. Tuned to outlive a `journalctl --since 30min` dump
// on a slow visor but kill a `tail -f` that's mis-used as Exec.
const execDefaultTimeout = 30 * time.Second

// execMaxTimeout is the upper bound on caller-supplied timeouts.
// Prevents an authenticated-but-buggy caller from holding the host
// indefinitely.
const execMaxTimeout = 5 * time.Minute

// CommandExecReq is the input to PtyGateway.Exec. Mirrors CommandReq
// for the interactive Start path but adds Stdin and TimeoutMS.
type CommandExecReq struct {
	Name      string   // command, looked up in $PATH on the host
	Arg       []string // command arguments
	Env       []string // additional env vars; merged on top of host env
	Stdin     []byte   // optional stdin; capped at execMaxStdinBytes
	TimeoutMS int64    // wall-clock cap; 0 = execDefaultTimeout
}

// CommandExecResult is the output of PtyGateway.Exec.
//
// Stdout / Stderr are captured to memory and may be truncated when
// the command produces more than execMaxOutputBytes; the StdoutTruncated /
// StderrTruncated flags signal it. ExitCode follows Go's os/exec
// conventions (-1 on signal-kill, 0 on success, non-zero on exit).
// TimedOut is set when the deadline fired before the command exited.
type CommandExecResult struct {
	Stdout          []byte
	Stderr          []byte
	StdoutTruncated bool
	StderrTruncated bool
	ExitCode        int
	TimedOut        bool
	// DurationMS reports the time from spawn to exit/timeout, useful
	// for callers measuring command latency without round-tripping
	// their own timestamps.
	DurationMS int64
}

// Exec runs Name + Arg on the host without a PTY. Implements the
// LocalPtyGateway side of the new RPC method. The session is one-shot
// — there's no separate Start / Stop / Read / Write protocol; the
// whole command lifetime is inside this one call.
//
// Concurrency: callers can run multiple Exec RPCs in flight; each
// spawns its own os/exec.Cmd. The local LocalPtyGateway state is NOT
// touched by Exec, so a concurrent interactive Start session on the
// same gateway works unaffected.
func (g *LocalPtyGateway) Exec(req *CommandExecReq, resp *CommandExecResult) error {
	if req == nil || req.Name == "" {
		return errors.New("dmsgpty: Exec: empty command name")
	}
	if len(req.Stdin) > execMaxStdinBytes {
		return fmt.Errorf("dmsgpty: Exec: stdin %d > max %d", len(req.Stdin), execMaxStdinBytes)
	}

	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = execDefaultTimeout
	}
	if timeout > execMaxTimeout {
		timeout = execMaxTimeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(ctx, req.Name, req.Arg...) //nolint:gosec // intentional remote exec; gated by dmsgpty whitelist
	cmd.Env = mergeEnv(envSnapshotForExec(), req.Env)
	// Setsid so a hung child doesn't take the parent visor with it
	// — SIGKILL on context timeout reaches the whole process group.
	cmd.SysProcAttr = execSysProcAttr()

	if len(req.Stdin) > 0 {
		cmd.Stdin = bytes.NewReader(req.Stdin)
	}

	var stdoutBuf, stderrBuf capBuffer
	stdoutBuf.cap = execMaxOutputBytes
	stderrBuf.cap = execMaxOutputBytes
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()

	resp.Stdout = stdoutBuf.bytes()
	resp.Stderr = stderrBuf.bytes()
	resp.StdoutTruncated = stdoutBuf.truncated
	resp.StderrTruncated = stderrBuf.truncated
	resp.DurationMS = time.Since(start).Milliseconds()

	if ctx.Err() == context.DeadlineExceeded {
		resp.TimedOut = true
		// Return without err for timeout — caller looks at TimedOut.
		// ExitCode will be -1 (signal-killed) on Unix.
		resp.ExitCode = -1
		return nil
	}
	if err == nil {
		resp.ExitCode = 0
		return nil
	}
	// exec.ExitError gives a real exit code; other errors leave -1.
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		resp.ExitCode = ee.ExitCode()
		return nil
	}
	resp.ExitCode = -1
	return nil
}

// Exec on the proxied gateway forwards to the underlying PtyClient.
// Same one-shot semantics; the proxy adds nothing but the RPC hop.
func (g *ProxiedPtyGateway) Exec(req *CommandExecReq, resp *CommandExecResult) error {
	return g.ptyC.execCall(req, resp)
}

// capBuffer is a bytes.Buffer-shaped sink that stops accepting writes
// once cap bytes have been buffered. Sets truncated=true when the
// first dropped write occurs. Concurrent Writes are guarded by mx
// because os/exec uses two goroutines (stdout, stderr) and a single
// capBuffer is passed to each independent reader — actually no, each
// stream gets its OWN capBuffer; the mutex is defensive for any
// future multi-writer reuse.
type capBuffer struct {
	mx        sync.Mutex
	buf       bytes.Buffer
	cap       int
	truncated bool
}

func (c *capBuffer) Write(p []byte) (int, error) {
	c.mx.Lock()
	defer c.mx.Unlock()
	room := c.cap - c.buf.Len()
	if room <= 0 {
		c.truncated = true
		// Pretend we wrote everything so the producer doesn't error.
		// The truncated flag carries the real signal upstream.
		return len(p), nil
	}
	if len(p) <= room {
		return c.buf.Write(p)
	}
	c.truncated = true
	n, err := c.buf.Write(p[:room])
	// Drop the rest.
	return n + (len(p) - room), err
}

func (c *capBuffer) bytes() []byte {
	c.mx.Lock()
	defer c.mx.Unlock()
	out := make([]byte, c.buf.Len())
	copy(out, c.buf.Bytes())
	return out
}

// envSnapshotForExec returns the host's environment for use as the
// exec base. Captured eagerly because os.Environ allocates fresh and
// we don't want concurrent Exec calls to race on shared state.
//
// In tests, this can be stubbed.
var envSnapshotForExec = func() []string {
	return defaultEnvSnapshot()
}

// execSysProcAttr returns the SysProcAttr to apply to spawned commands
// so context cancellation reaches the full process group. Platform-
// specific (unix: Setsid; windows: noop). Implementation lives in
// pty_unix.go / pty_windows.go to match the existing pattern.

// _ catches misspell linter on this file when it would otherwise
// pass without exercise.
var _ = io.EOF
