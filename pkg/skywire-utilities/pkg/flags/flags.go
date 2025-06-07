// Package flags internal/flags/flags.go
package flags

import (
	cc "github.com/ivanpirog/coloredcobra"
	"github.com/spf13/cobra"
)

// InitFlags is used to set command flags for help menu and styling with coloredcobra
func InitFlags(cmd *cobra.Command, usage bool) {
	var helpflag bool
	if !usage {
		cmd.SetUsageTemplate(help)
	} else {
		cmd.SetUsageTemplate(helpUsage)
	}
	cmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "show help menu")
	cmd.SetHelpCommand(&cobra.Command{Hidden: true})
	cmd.PersistentFlags().MarkHidden("help") //nolint

	cc.Init(&cc.Config{
		RootCmd:         cmd,
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
}

const help = "{{if .HasAvailableSubCommands}}{{end}} {{if gt (len .Aliases) 0}}\r\n\r\n" +
	"{{.NameAndAliases}}{{end}}{{if .HasAvailableSubCommands}}" +
	"Available Commands:{{range .Commands}}  {{if and (ne .Name \"completion\") .IsAvailableCommand}}\r\n  " +
	"{{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}\r\n\r\n" +
	"Flags:\r\n" +
	"{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}\r\n\r\n" +
	"Global Flags:\r\n" +
	"{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}\r\n\r\n"

var helpUsage = "Usage:\r\n" +
	"  {{.UseLine}}{{if .HasAvailableSubCommands}}{{end}} {{if gt (len .Aliases) 0}}\r\n\r\n" + help
