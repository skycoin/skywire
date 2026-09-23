//go:build js

// Package cliconfig cmd/skywire-cli/commands/config/skyenvpath_js.go c4-vis-cli
package cliconfig

import (
	"os"

	skyenvpath "github.com/skycoin/skywire/pkg/skywireconfig/skyenvfile"
)

// resolveSkyenvFile names the SKYENV file whose values become this
// command's flag DEFAULTS.
//
// The browser build has no subprocesses: `skywire autoconfig` re-enters
// `config gen` in THIS process, and sets SKYENV right before it does
// (autoconfig_exec_js.go) for parity with the environment the native
// path hands its child. That set comes too late. The defaults below are
// read in this package's var/init phase, which is over before any
// command runs — so gen read an empty SKYENV and sourced no file at
// all, and every value the operator had in /etc/skywire.conf was
// invisible to it.
//
// Found with WISP: `autoconfig --wisp` wrote WISP=true to the conf
// correctly, the next `autoconfig` read it back correctly, and the
// config it generated still had no wisp section, because gen never saw
// the file. It is not a wisp bug — it was every SKYENV setting that no
// flag on the SAME run also carried.
//
// So resolve the default path here, the way autoconfig does when SKYENV
// is unset. A path that does not exist is not an error: the skyenv
// parser reports a missing file as "no variables set", which is the
// built-in defaults, which is what gen did before.
func resolveSkyenvFile() string {
	if p := os.Getenv("SKYENV"); p != "" {
		return p
	}
	return skyenvpath.DefaultPath()
}
