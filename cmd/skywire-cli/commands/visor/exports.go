// Package clivisor cmd/skywire-cli/commands/visor/exports.go
//
// Exposes visor subcommand handlers so the top-level shortcuts
// (cli pk, cli status, cli halt) can delegate Run without sharing
// the *cobra.Command instance. Each shortcut wraps one of these so
// the help template renders correctly under both parents — root and
// `cli visor` use different group taxonomies, and a single Command
// can only carry one GroupID.
package clivisor

// PKCmd is the `cli visor pk` command. Top-level `cli pk` reuses its Run.
var PKCmd = pkCmd

// PKDNSLabelCmd is the `cli visor pk dnslabel` subcommand. Top-level
// `cli pk dnslabel` re-mounts the same Command so both paths reach it.
var PKDNSLabelCmd = pkDNSLabelCmd

// SummaryCmd is the `cli visor info` command. Top-level `cli status`
// reuses its Run; the leaf name differs only at the root.
var SummaryCmd = summaryCmd

// HaltCmd is the `cli visor halt` command. Top-level `cli halt`
// reuses its Run.
var HaltCmd = shutdownCmd
