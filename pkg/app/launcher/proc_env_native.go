//go:build !tinygo && !js

// Package launcher pkg/app/launcher/proc_env_native.go c2-app-launcher
package launcher

import "github.com/skycoin/skywire/pkg/app/appserver"

// procCmdEnv returns the environment the proc's underlying exec.Cmd was
// started with, so RestartApp can hand the same env to the replacement.
// Split per-target because Proc.Cmd() is *exec.Cmd only on native builds
// (tinygo/js have no exec and type it as any).
func procCmdEnv(p *appserver.Proc) []string {
	return p.Cmd().Env
}
