// Package commands cmd/skywire-cli/commands/root.go
package commands

import (
	"fmt"
	"log"
	"strings"

	"github.com/bitfield/script"
	cc "github.com/ivanpirog/coloredcobra"
	"github.com/pterm/pterm"
	"github.com/pterm/pterm/putils"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	clicompletion "github.com/skycoin/skywire/cmd/skywire-cli/commands/completion"
	cliconfig "github.com/skycoin/skywire/cmd/skywire-cli/commands/config"
	clidmsgget "github.com/skycoin/skywire/cmd/skywire-cli/commands/dmsgget"
	clidmsgpty "github.com/skycoin/skywire/cmd/skywire-cli/commands/dmsgpty"
	climdisc "github.com/skycoin/skywire/cmd/skywire-cli/commands/mdisc"
	clireward "github.com/skycoin/skywire/cmd/skywire-cli/commands/reward"
	clirtfind "github.com/skycoin/skywire/cmd/skywire-cli/commands/rtfind"
	clisurvey "github.com/skycoin/skywire/cmd/skywire-cli/commands/survey"
	clivisor "github.com/skycoin/skywire/cmd/skywire-cli/commands/visor"
	clivpn "github.com/skycoin/skywire/cmd/skywire-cli/commands/vpn"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
)

var rootCmd = &cobra.Command{
	Use:   "skywire-cli",
	Short: "Command Line Interface for skywire",
	Long: `
	┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐  ┌─┐┬  ┬
	└─┐├┴┐└┬┘││││├┬┘├┤───│  │  │
	└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘  └─┘┴─┘┴`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
}

var treeCmd = &cobra.Command{
	Use:                   "tree",
	Short:                 "subcommand tree",
	Long:                  `subcommand tree`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		// You can use a LeveledList here, for easy generation.
		leveledList := pterm.LeveledList{}
		leveledList = append(leveledList, pterm.LeveledListItem{Level: 0, Text: rootCmd.Use})
		for _, j := range rootCmd.Commands() {
			use := strings.Split(j.Use, " ")
			leveledList = append(leveledList, pterm.LeveledListItem{Level: 1, Text: use[0]})
			for _, k := range j.Commands() {
				use := strings.Split(k.Use, " ")
				leveledList = append(leveledList, pterm.LeveledListItem{Level: 2, Text: use[0]})
				for _, l := range k.Commands() {
					use := strings.Split(l.Use, " ")
					leveledList = append(leveledList, pterm.LeveledListItem{Level: 3, Text: use[0]})
					for _, m := range l.Commands() {
						use := strings.Split(m.Use, " ")
						leveledList = append(leveledList, pterm.LeveledListItem{Level: 4, Text: use[0]})
						for _, n := range m.Commands() {
							use := strings.Split(n.Use, " ")
							leveledList = append(leveledList, pterm.LeveledListItem{Level: 5, Text: use[0]})
							for _, o := range n.Commands() {
								use := strings.Split(o.Use, " ")
								leveledList = append(leveledList, pterm.LeveledListItem{Level: 6, Text: use[0]})
							}
						}
					}
				}
			}
		}
		// Generate tree from LeveledList.
		r := putils.TreeFromLeveledList(leveledList)

		// Render TreePrinter
		err := pterm.DefaultTree.WithRoot(r).Render()
		if err != nil {
			log.Fatal("render subcommand tree: ", err)
		}
	},
}

var docCmd = &cobra.Command{
	Use:   "doc",
	Short: "gnerate markdown docs",
	Long: `generate markdown docs

	UNHIDEFLAGS=1 skywire-cli doc`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("\n# %s\n", "skywire-cli documentation")
		fmt.Printf("\n%s\n", "skywire command line interface")

		fmt.Printf("\n## %s\n", rootCmd.Use)
		fmt.Printf("\n```\n")
		rootCmd.Help() //nolint
		fmt.Printf("\n```\n")
		fmt.Printf("\n## %s\n", "global flags")
		fmt.Printf("\n%s\n", "The skywire-cli interacts with the running visor via rpc calls. By default the rpc server is available on localhost:3435. The rpc address and port the visor is using may be changed in the config file, once generated.")

		fmt.Printf("\n%s\n", "It is not recommended to expose the rpc server on the local network. Exposing the rpc allows unsecured access to the machine over the local network")
		fmt.Printf("\n```\n")
		fmt.Printf("\n%s\n", "Global Flags:")
		fmt.Printf("\n%s\n", "			--rpc string   RPC server address (default \"localhost:3435\")")
		fmt.Printf("\n%s\n", "			--json bool   print output as json")
		fmt.Printf("\n```\n")

		fmt.Printf("\n## %s\n", "subcommand tree")
		fmt.Printf("\n%s\n", "A tree representation of the skywire-cli subcommands")
		fmt.Printf("\n```\n")
		//_, _ = script.Exec(`go run cmd/skywire-cli/skywire-cli.go tree`).Stdout() //nolint
		fmt.Printf("\n```\n")

		var use string
		for _, j := range rootCmd.Commands() {
			use = strings.Split(j.Use, " ")[0]
			fmt.Printf("\n### %s\n", use)
			fmt.Printf("\n```\n")
			j.Help() //nolint
			fmt.Printf("\n```\n")
			if j.Name() == "survey" {
				fmt.Printf("\n```\n")
				_, _ = script.Exec(`sudo go run cmd/skywire-cli/skywire-cli.go survey`).Stdout() //nolint
				fmt.Printf("\n```\n")
			}
			for _, k := range j.Commands() {
				use = strings.Split(j.Use, " ")[0] + " " + strings.Split(k.Use, " ")[0]
				fmt.Printf("\n#### %s\n", use)
				fmt.Printf("\n```\n")
				k.Help() //nolint
				fmt.Printf("\n```\n")
				if k.Name() == "gen" {
					fmt.Printf("\n```\n")
					fmt.Printf("$ skywire-cli config gen -bpirxn\n")
					_, _ = script.Exec(`go run cmd/skywire-cli/skywire-cli.go config gen -n`).Stdout() //nolint
					fmt.Printf("\n```\n")
				}
				for _, l := range k.Commands() {
					use = strings.Split(j.Use, " ")[0] + " " + strings.Split(k.Use, " ")[0] + " " + strings.Split(l.Use, " ")[0]
					fmt.Printf("\n##### %s\n", use)
					fmt.Printf("\n```\n")
					l.Help() //nolint
					fmt.Printf("\n```\n")
					for _, m := range l.Commands() {
						use = strings.Split(j.Use, " ")[0] + " " + strings.Split(k.Use, " ")[0] + " " + strings.Split(l.Use, " ")[0] + " " + strings.Split(m.Use, " ")[0]
						fmt.Printf("\n###### %s\n", use)
						fmt.Printf("\n```\n")
						m.Help() //nolint
						fmt.Printf("\n```\n")
					}
				}
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(
		cliconfig.RootCmd,
		clidmsgpty.RootCmd,
		clivisor.RootCmd,
		clivpn.RootCmd,
		clireward.RootCmd,
		clisurvey.RootCmd,
		clirtfind.RootCmd,
		climdisc.RootCmd,
		clicompletion.RootCmd,
		clidmsgget.RootCmd,
		treeCmd,
		docCmd,
	)
	var jsonOutput bool
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, internal.JSONString, false, "print output in json")
	rootCmd.PersistentFlags().MarkHidden(internal.JSONString) //nolint
	var helpflag bool
	rootCmd.SetUsageTemplate(help)
	rootCmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "help for "+rootCmd.Use)
	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	rootCmd.PersistentFlags().MarkHidden("help") //nolint
}

// Execute executes root CLI command.
func Execute() {
	cc.Init(&cc.Config{
		RootCmd:       rootCmd,
		Headings:      cc.HiBlue + cc.Bold, //+ cc.Underline,
		Commands:      cc.HiBlue + cc.Bold,
		CmdShortDescr: cc.HiBlue,
		Example:       cc.HiBlue + cc.Italic,
		ExecName:      cc.HiBlue + cc.Bold,
		Flags:         cc.HiBlue + cc.Bold,
		//FlagsDataType: cc.HiBlue,
		FlagsDescr:      cc.HiBlue,
		NoExtraNewlines: true,
		NoBottomNewline: true,
	})
	if err := rootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
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
