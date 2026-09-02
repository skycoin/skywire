//go:build tinygo || js

// Package launcher pkg/app/launcher/proc_env_tinygo.go c2-app-launcher
package launcher

import "github.com/skycoin/skywire/pkg/app/appserver"

// procCmdEnv: no exec.Cmd on tinygo/js — a restarted in-process app carries
// no inherited process environment.
func procCmdEnv(*appserver.Proc) []string {
	return nil
}
