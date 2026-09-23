//go:build !js

// Package cliconfig cmd/skywire-cli/commands/config/skyenvpath.go c4-vis-cli
package cliconfig

import "os"

// resolveSkyenvFile names the SKYENV file whose values become this
// command's flag DEFAULTS. Native `config gen` is always reached
// either directly by an operator — who names the file with SKYENV or
// accepts the built-in defaults — or as a subprocess of `skywire
// autoconfig`, which passes SKYENV in the child's environment. Both
// arrive here with the variable already set, so reading it is enough.
func resolveSkyenvFile() string { return os.Getenv("SKYENV") }
