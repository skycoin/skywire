//go:build !(js && wasm)

// Package putils cmd/skywire-cli/cliutil/putils/putils_native.go c5-cli-util
//
// Shadow of github.com/pterm/pterm/putils for the one helper the CLI uses;
// see the cliutil/pterm shadow for the rationale.
package putils

import "github.com/pterm/pterm/putils"

// TreeFromLeveledList is putils.TreeFromLeveledList.
var TreeFromLeveledList = putils.TreeFromLeveledList
