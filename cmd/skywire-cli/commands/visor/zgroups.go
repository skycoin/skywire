// Package clivisor cmd/skywire-cli/commands/visor/zgroups.go c4-vis-cli
//
// Groups the subcommands of `skywire cli visor` so the help output
// renders as discoverable sections instead of a flat wall. Filename
// starts with `z` so this init runs AFTER the per-feature files'
// inits (Go runs init functions in the order source files are
// compiled, alphabetically within a package) — by then every
// subcommand has been AddCommand'd and we can assign GroupIDs by
// name in one place.
//
// Every non-hidden, non-deprecated subcommand MUST appear in the
// commandGroup map below. The shared help template in pkg/flags
// (helpTemplateNoUsage / help) renders ONLY grouped commands once any
// group is defined — it does NOT emit cobra's default "Additional
// Commands:" block — so a command left out of the map is registered
// and callable but invisible in `visor --help`. Keep this map in sync
// when adding a subcommand.
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
	"suspend":      groupLifecycle,
	"resume":       groupLifecycle,
	"suspended":    groupLifecycle,
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
	"cxo":          groupSubsystems,
	"pair":         groupSubsystems,
	"log":          groupDiagnostics,
	"go":           groupDiagnostics,
	"goroutines":   groupDiagnostics,
	"ping":         groupDiagnostics,
	"doctor":       groupDiagnostics,
	"whois":        groupDiagnostics,
	"uptime":       groupDiagnostics,
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
