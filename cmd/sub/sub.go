// Package main cmd/sub/sub.go
//
// Standalone (dmsg-only) UDP→dmsg bridge ("sub"). Length-prefixed
// UDP datagrams ferried over a dmsg stream. Plan B for
// UDP-over-skynet — for non-realtime UDP (DNS, NTP, SNMP, MQTT-SN,
// WireGuard control plane). For media-class UDP use Plan A
// (packet-level UDP at the route-group layer; RFC #2607).
//
// Reachable from a unified skywire binary as `skywire dmsg sub`.
package main

import (
	cc "github.com/ivanpirog/coloredcobra"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/cmd/sub/commands"
)

func init() {
	var helpflag bool
	commands.RootCmd.SetUsageTemplate(help)
	commands.RootCmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "help menu")
	commands.RootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	commands.RootCmd.PersistentFlags().MarkHidden("help") //nolint:errcheck,gosec
}

func main() {
	cc.Init(&cc.Config{
		RootCmd:         commands.RootCmd,
		Headings:        cc.HiBlue + cc.Bold,
		Commands:        cc.HiBlue + cc.Bold,
		CmdShortDescr:   cc.HiBlue,
		Example:         cc.HiBlue + cc.Italic,
		ExecName:        cc.HiBlue + cc.Bold,
		Flags:           cc.HiBlue + cc.Bold,
		FlagsDescr:      cc.HiBlue,
		NoExtraNewlines: true,
		NoBottomNewline: true,
	})
	commands.Execute()
}

const help = "Usage:\r\n" +
	"  {{.UseLine}}{{if .HasAvailableSubCommands}}{{end}} {{if gt (len .Aliases) 0}}\r\n\r\n" +
	"{{.NameAndAliases}}{{end}}{{if .HasAvailableSubCommands}}\r\n\r\n" +
	"Available Commands:{{range .Commands}}{{if (or .IsAvailableCommand)}}\r\n  " +
	"{{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}\r\n\r\n" +
	"Flags:\r\n" +
	"{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}\r\n\r\n" +
	"Global Flags:\r\n" +
	"{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}\r\n\r\n"
