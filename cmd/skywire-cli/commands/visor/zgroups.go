// Package clivisor cmd/skywire-cli/commands/visor/zgroups.go
//
// Groups the 17 subcommands of `skywire cli visor` so the help output
// renders as discoverable sections instead of a flat wall. Filename
// starts with `z` so this init runs AFTER the per-feature files'
// inits (Go runs init functions in the order source files are
// compiled, alphabetically within a package) — by then every
// subcommand has been AddCommand'd and we can assign GroupIDs by
// name in one place.
package clivisor

import (
	"github.com/spf13/cobra"
)

// Group IDs for visor subcommands.
const (
	groupLifecycle   = "lifecycle"
	groupIdentity    = "identity"
	groupNetwork     = "network"
	groupSubsystems  = "subsystems"
	groupDiagnostics = "diagnostics"
)

// commandGroup maps a command name to its group ID. Unlisted commands
// fall through to cobra's "Additional Commands:" bucket.
var commandGroup = map[string]string{
	"start":        groupLifecycle,
	"halt":         groupLifecycle,
	"reinit":       groupLifecycle,
	"ready":        groupLifecycle,
	"pk":           groupIdentity,
	"info":         groupIdentity,
	"ver":          groupIdentity,
	"user":         groupIdentity,
	"ports":        groupNetwork,
	"dmsg-servers": groupNetwork,
	"ip":           groupNetwork,
	"app":          groupSubsystems,
	"hv":           groupSubsystems,
	"proxies":      groupSubsystems,
	"reward":       groupSubsystems,
	"log":          groupDiagnostics,
	"go":           groupDiagnostics,
	"ping":         groupDiagnostics,
}

func init() {
	RootCmd.AddGroup(
		&cobra.Group{ID: groupLifecycle, Title: "Lifecycle:"},
		&cobra.Group{ID: groupIdentity, Title: "Identity & version:"},
		&cobra.Group{ID: groupNetwork, Title: "Network state:"},
		&cobra.Group{ID: groupSubsystems, Title: "Subsystems:"},
		&cobra.Group{ID: groupDiagnostics, Title: "Diagnostics:"},
	)
	for _, c := range RootCmd.Commands() {
		if g, ok := commandGroup[c.Name()]; ok {
			c.GroupID = g
		}
	}
}
