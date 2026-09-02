//go:build js

// Package commands cmd/skywire/commands/autoconfig_exec_js.go c4-vis-cli
//
// js/wasm half of the autoconfig process-management seam. The browser
// has no subprocesses and no init system, so the native strategy —
// re-invoke this binary for config gen and hand the visor to systemd —
// doesn't translate. Instead:
//
//   - config gen re-enters the command tree IN-PROCESS (same cobra
//     root, fresh args), which behaves identically because the gen
//     command keeps no state autoconfig depends on;
//   - the version and reward-address lookups read their sources
//     directly (same binary, same virtual filesystem);
//   - finishAutoconfig starts the visor in the FOREGROUND as the
//     very last step, after the summary prints. That is the browser
//     equivalent of the systemd restart: the terminal that ran
//     `skywire autoconfig` becomes the visor's terminal, with the
//     same logging an operator would see from `skywire visor -c …`
//     on Linux. A second terminal (fresh wasm instance) reaches it
//     over the virtual loopback with `skywire cli`.
//
// The SKYENV file (/etc/skywire.conf) is honored exactly as on
// Linux: pkg/cmdutil's pure-Go parser sources it from the virtual
// filesystem, and the page seeds it with PKGENV=true the way the
// Linux packages ship it.
package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// selfVersion reports this binary's version. Same process — no need
// to shell out to `skywire -v`.
func selfVersion() ([]byte, error) {
	return []byte("skywire " + buildinfo.Version()), nil
}

// selfRewardAddress reads the reward-address file directly — the same
// file `skywire cli reward -r` prints.
func selfRewardAddress() ([]byte, error) {
	b, err := os.ReadFile(visorconfig.PackageConfig().LocalPath + "/" + skyenv.RewardFile)
	if err != nil {
		return nil, err
	}
	return []byte(strings.TrimSpace(string(b))), nil
}

// extraGenArgs appends --nofetch under js: the services-config URL is
// not reachable cross-origin from a browser page, and the fetch
// failure path lands on the embedded defaults anyway — skip the stall.
func extraGenArgs() []string { return []string{"--nofetch"} }

// execConfigGen re-enters the command tree in-process with the
// assembled `cli config gen` args. SKYENV is exported first so gen
// sources the same env file autoconfig resolved (subprocess parity
// with the native path, which passes it via cmd.Env).
func execConfigGen(r resolvedConfig, args []string) error {
	if r.skyenvPath != "" {
		if err := os.Setenv("SKYENV", r.skyenvPath); err != nil {
			return err
		}
	}
	RootCmd.SetArgs(args)
	return RootCmd.Execute()
}

// restartOrPrompt under js only explains what happens next — the
// actual start is deferred to finishAutoconfig so the summary
// (public key, hypervisor URL, reward address) prints BEFORE the
// visor takes over the terminal.
func restartOrPrompt(r resolvedConfig) {
	if r.noRestart {
		msg2(fmt.Sprintf("Config applied WITHOUT starting the visor (--no-restart).\n\tStart it with:\n\t%sskywire visor -c %s%s",
			colorRed, r.configPath, colorReset))
		return
	}
	msg3("No init system in the browser — the visor starts in the foreground after this summary.")
}

// finishAutoconfig starts the visor in the foreground. This call does
// not return until the visor exits (halt or fatal error) — exactly
// like typing `skywire visor -c <path>` as the next command.
func finishAutoconfig(r resolvedConfig) {
	if r.noRestart {
		return
	}
	msg2(fmt.Sprintf("Starting visor in the foreground:\n  %sskywire visor -c %s%s", colorCyan, r.configPath, colorReset))
	RootCmd.SetArgs([]string{"visor", "-c", r.configPath})
	if err := RootCmd.Execute(); err != nil {
		fmt.Printf("%s>>> FATAL:%s visor exited with error: %v\n", colorRed, colorReset, err)
		os.Exit(1)
	}
}
