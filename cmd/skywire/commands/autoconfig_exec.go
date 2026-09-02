//go:build !js

// Package commands cmd/skywire/commands/autoconfig_exec.go c4-vis-cli
//
// Native half of the autoconfig process-management seam. On a real OS
// autoconfig delegates to subprocesses (this same binary re-invoked
// for config gen / reward / -v) and to systemctl for the service
// lifecycle. The js/wasm twin (autoconfig_exec_js.go) has neither
// subprocesses nor an init system: it re-enters the command tree
// in-process and starts the visor in the foreground.
package commands

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// skywireBin returns the absolute path to the running skywire binary so the
// sub-invocations below (config gen, reward, -v) target THIS binary rather than
// a bare "skywire" looked up on PATH. That matters on Windows: the MSI runs
// autoconfig as a deferred CustomAction at install time, when the install dir is
// not yet on PATH, so a bare "skywire" would not resolve. Falls back to "skywire"
// only if os.Executable() fails (e.g. an exotic platform), preserving old behavior.
func skywireBin() string {
	if exe, err := os.Executable(); err == nil && exe != "" {
		return exe
	}
	return "skywire"
}

// selfVersion reports this binary's version the way `skywire -v` prints it.
func selfVersion() ([]byte, error) {
	return exec.Command(skywireBin(), "-v").Output() //nolint:gosec
}

// selfRewardAddress reads the configured reward address via
// `skywire cli reward -r`.
func selfRewardAddress() ([]byte, error) {
	return exec.Command(skywireBin(), "cli", "reward", "-r").Output() //nolint:gosec
}

// extraGenArgs contributes no extra config-gen args on native builds.
func extraGenArgs() []string { return nil }

// execConfigGen runs `skywire cli config gen` as a subprocess with the
// assembled args.
//
// Suppress the subprocess's stdout/stderr by default. The DMSG
// fetch chatter ([INFO]/[DEBUG] lines) and -w's hidden JSON dump
// both go through here and are noise on the happy path. Buffer
// stderr; on failure flush it so the operator can see what
// broke (missing service-config.json, bad SK, write permission
// denied, etc.). If the operator wants the verbose path, the
// printableArgs shown by generateConfig are the same command
// without -w to paste into a terminal.
func execConfigGen(r resolvedConfig, args []string) error {
	var stderrBuf bytes.Buffer
	cmd := exec.Command(skywireBin(), args...) //nolint:gosec
	cmd.Stdout = os.Stdout
	cmd.Stderr = &stderrBuf
	cmd.Env = os.Environ()
	if r.skyenvPath != "" {
		cmd.Env = append(cmd.Env, "SKYENV="+r.skyenvPath)
	}
	if err := cmd.Run(); err != nil {
		if stderrBuf.Len() > 0 {
			fmt.Fprint(os.Stderr, stderrBuf.String())
		}
		return err
	}
	return nil
}

// restartOrPrompt branches on the resolved mode to pick the right
// service-manager invocation. Linux: systemctl. Windows: defer to
// Phase 2 — for now we just tell the operator the manual command
// they need (`skywire visor -c <path>`) until the Windows Service
// registration ships.
//
// Active unit → restart; inactive → print the start instructions.
// Never tries to auto-enable: respects operator intent.
func restartOrPrompt(r resolvedConfig) {
	if runtime.GOOS == "windows" {
		msg2(fmt.Sprintf("Start skywire on Windows with (admin PowerShell):\n\t%sskywire visor -c %q%s",
			colorRed, r.configPath, colorReset))
		return
	}

	// pty-safe path: the operator asked us NOT to restart. This is for
	// updating over the visor's own dmsgpty, where a `systemctl restart
	// skywire` would tear down the pty session (and this very process)
	// mid-restart. Config is already written; the operator applies it
	// out-of-band, decoupled from the session, as the LAST step.
	if r.noRestart {
		startCmd := "systemctl start --no-block skywire-autoconfig.service"
		if r.useUserUnit {
			startCmd = "systemctl --user start --no-block skywire-autoconfig.service"
		}
		msg2(fmt.Sprintf("Config applied WITHOUT restarting skywire (--no-restart).\n\tApply it as your LAST command (safe over dmsgpty):\n\t%s%s%s",
			colorRed, startCmd, colorReset))
		return
	}

	systemctlArgs := []string{}
	if r.useUserUnit {
		systemctlArgs = append(systemctlArgs, "--user")
	}

	// A running `hv serve` (the standalone wasm-visor page) serves the wasm build
	// EMBEDDED in the running binary, so it goes stale after a binary update until
	// its service restarts — the reason theskywirenetwork.net can lag develop.
	// Refresh it here as part of autoconfig, but only if it's ALREADY active
	// (never start it), and always as a system unit (it isn't a --user service).
	// A no-op when the unit isn't installed, so this is safe whether or not the
	// package ships skywire-wasm-visor-serve.service yet.
	if exec.Command("systemctl", "is-active", "--quiet", "skywire-wasm-visor-serve.service").Run() == nil { //nolint:gosec
		msg3("Restarting skywire-wasm-visor-serve (hv serve re-embeds the updated blob)…")
		_ = exec.Command("systemctl", "restart", "skywire-wasm-visor-serve.service").Run() //nolint:errcheck,gosec
	}

	checkArgs := append(append([]string{}, systemctlArgs...), "is-active", "--quiet", "skywire")
	if exec.Command("systemctl", checkArgs...).Run() == nil { //nolint:gosec
		restartArgs := append(append([]string{}, systemctlArgs...), "restart", "skywire")
		msg3("Restarting skywire service…")
		_ = exec.Command("systemctl", restartArgs...).Run() //nolint:errcheck,gosec
		return
	}

	startCmd := "systemctl"
	if r.useUserUnit {
		startCmd += " --user"
	}
	msg2(fmt.Sprintf("Start the skywire service with:\n\t%s%s enable --now skywire%s", colorRed, startCmd, colorReset))
}

// finishAutoconfig is a no-op on native builds — the service restart
// already happened (asynchronously) in restartOrPrompt.
func finishAutoconfig(_ resolvedConfig) {}
