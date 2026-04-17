// Package clitp cmd/skywire-cli/commands/tp/zgroups.go
//
// Groups the 13 subcommands of `skywire cli tp` so the help output
// renders as discoverable sections. See clivisor/zgroups.go for why
// this file is named with a leading `z` (init ordering).
package clitp

import (
	"github.com/spf13/cobra"
)

// Group IDs for tp subcommands.
const (
	groupLocal   = "local"
	groupNetwork = "network"
	groupConfig  = "config"
)

// commandGroup maps a command name to its group ID.
var commandGroup = map[string]string{
	"add":        groupLocal,
	"rm":         groupLocal,
	"disc":       groupLocal,
	"tree":       groupNetwork,
	"v":          groupNetwork,
	"net-stats":  groupNetwork,
	"tpd-stats":  groupNetwork,
	"tpd-health": groupNetwork,
	"metrics":    groupNetwork,
	"uptime":     groupNetwork,
	"auto":       groupConfig,
	"sync":       groupConfig,
	"id":         groupConfig,
}

func init() {
	RootCmd.AddGroup(
		&cobra.Group{ID: groupLocal, Title: "Local transports:"},
		&cobra.Group{ID: groupNetwork, Title: "Network queries:"},
		&cobra.Group{ID: groupConfig, Title: "Configuration:"},
	)
	for _, c := range RootCmd.Commands() {
		if g, ok := commandGroup[c.Name()]; ok {
			c.GroupID = g
		}
	}
}
