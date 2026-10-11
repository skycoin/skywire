//go:build !js

// Package launcher pkg/app/launcher/proc_env_native.go c2-app-launcher
package launcher

import "github.com/skycoin/skywire/pkg/app/appserver"

// procCmdEnv returns the environment the proc's underlying exec.Cmd was
// started with, so RestartApp can hand the same env to the replacement.
// Split per-target because Proc.Cmd() is *exec.Cmd only on native builds
// (tinygo/js have no exec and type it as any).
//
// A built-in app runs in-process and has no exec.Cmd, so it has no process
// environment to carry over — the same answer the tinygo/js build gives.
// Dereferencing that nil Cmd made `skywire cli visor app restart
// skysocks-client` panic and take the whole visor down.
func procCmdEnv(p *appserver.Proc) []string {
	if p == nil {
		return nil
	}
	cmd := p.Cmd()
	if cmd == nil {
		return nil
	}
	return cmd.Env
}
