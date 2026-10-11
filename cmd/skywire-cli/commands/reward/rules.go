// Package clireward cmd/skywire-cli/commands/reward/rules.go c4-vis-cli
package clireward

import (
	"fmt"
	"os"
	"regexp"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/skycoin/skywire/rewards"
)

var asHTML bool
var rawFile bool

func init() {
	rewardCmd.AddCommand(rulesCmd)
	rulesCmd.Flags().BoolVarP(&asHTML, "html", "l", false, "render html from markdown")
	rulesCmd.Flags().BoolVarP(&rawFile, "raw", "r", false, "print raw the embedded file")
}

var rulesCmd = &cobra.Command{
	Use:   "rules",
	Short: "display the mainnet reward eligibility rules",
	Long: `Display the mainnet reward eligibility rules (embedded at build time).

By default the markdown is rendered for the terminal. Use --raw to print
the original markdown source, or --html to render it as an HTML fragment.`,
	Run: func(_ *cobra.Command, _ []string) {
		if rawFile {
			fmt.Println(rewards.MainnetRules)
			os.Exit(0)
		}
		if asHTML {
			// Preprocess to replace ~text~ with ~~text~~ for strikethrough
			re := regexp.MustCompile(`~(.*?)~`)
			rules := re.ReplaceAllString(rewards.MainnetRules, "~~$1~~")
			out, err := rulesHTML(rules)
			if err != nil {
				fmt.Println("Error rendering markdown as HTML:", err)
				os.Exit(1)
			}
			fmt.Println(out)
			os.Exit(0)
		}
		terminalWidth, _, err := term.GetSize(int(os.Stdout.Fd()))
		if err != nil {
			terminalWidth = 80
		}
		leftPad := 6
		fmt.Printf("%s\n", rulesTerminal(rewards.MainnetRules, terminalWidth, leftPad))
	},
}
