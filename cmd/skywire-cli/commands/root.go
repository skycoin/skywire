// Package commands cmd/skywire-cli/commands/root.go
package commands

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/bitfield/script"
	"github.com/pterm/pterm"
	"github.com/pterm/pterm/putils"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	clicompletion "github.com/skycoin/skywire/cmd/skywire-cli/commands/completion"
	cliconfig "github.com/skycoin/skywire/cmd/skywire-cli/commands/config"
	clidmsgpty "github.com/skycoin/skywire/cmd/skywire-cli/commands/dmsgpty"
	clilog "github.com/skycoin/skywire/cmd/skywire-cli/commands/log"
	climdisc "github.com/skycoin/skywire/cmd/skywire-cli/commands/mdisc"
	cliskysocksc "github.com/skycoin/skywire/cmd/skywire-cli/commands/proxy"
	clireward "github.com/skycoin/skywire/cmd/skywire-cli/commands/reward"
	clirewards "github.com/skycoin/skywire/cmd/skywire-cli/commands/rewards"
	cliroute "github.com/skycoin/skywire/cmd/skywire-cli/commands/route"

	clinet "github.com/skycoin/skywire/cmd/skywire-cli/commands/net"
	clisurvey "github.com/skycoin/skywire/cmd/skywire-cli/commands/survey"
	clitp "github.com/skycoin/skywire/cmd/skywire-cli/commands/tp"
	cliut "github.com/skycoin/skywire/cmd/skywire-cli/commands/ut"
	clivisor "github.com/skycoin/skywire/cmd/skywire-cli/commands/visor"
	clivpn "github.com/skycoin/skywire/cmd/skywire-cli/commands/vpn"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
)

func init() {
	RootCmd.AddCommand(
		cliconfig.RootCmd,
		clidmsgpty.RootCmd,
		clivisor.RootCmd,
		clivpn.RootCmd,
		cliut.RootCmd,
		clinet.RootCmd,
		clireward.RootCmd,
		clirewards.RootCmd,
		clisurvey.RootCmd,
		cliroute.RootCmd,
		clitp.RootCmd,
		climdisc.RootCmd,
		clicompletion.RootCmd,
		clilog.RootCmd,
		cliskysocksc.RootCmd,
		treeCmd,
		docCmd,
	)
	var jsonOutput bool
	RootCmd.PersistentFlags().BoolVar(&jsonOutput, internal.JSONString, false, "print output in json")
	RootCmd.PersistentFlags().MarkHidden(internal.JSONString) //nolint
}

// RootCmd is the root command for skywire-cli
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
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
		leveledList = append(leveledList, pterm.LeveledListItem{Level: 0, Text: RootCmd.Use})
		for _, j := range RootCmd.Commands() {
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

// for toc generation use: https://github.com/ekalinin/github-markdown-toc.go
var docCmd = &cobra.Command{
	Use:   "doc",
	Short: "generate markdown docs",
	Long: `generate markdown docs

	UNHIDEFLAGS=1 go run cmd/skywire-cli/skywire-cli.go doc

	UNHIDEFLAGS=1 go run cmd/skywire-cli/skywire-cli.go doc > cmd/skywire-cli/README1.md

	generate toc:

	cat cmd/skywire-cli/README1.md | gh-md-toc`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("\n# %s\n", "skywire-cli documentation")
		fmt.Printf("\n%s\n", "skywire command line interface")

		fmt.Printf("\n## %s\n", RootCmd.Use)
		fmt.Printf("\n```\n")
		RootCmd.Help() //nolint
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
		_, _ = script.Exec(`go run cmd/skywire-cli/skywire-cli.go tree`).Stdout() //nolint
		fmt.Printf("\n```\n")

		var use string
		for _, j := range RootCmd.Commands() {
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
					fmt.Printf("\n##### Example for package / msi\n")
					fmt.Printf("\n```\n")
					fmt.Printf("$ skywire-cli config gen -bpirxn --version 1.3.0\n")
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

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
