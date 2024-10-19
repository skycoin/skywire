// Package clireward cmd/skywire-cli/reward/rules.go
package clireward

import (
	"fmt"
	"os"

	markdown "github.com/MichaelMure/go-term-markdown"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/skycoin/skywire"
)

func init() {
	rewardCmd.AddCommand(rulesCmd)
}

var rulesCmd = &cobra.Command{
	Use:   "rules",
	Short: "display the mainnet rules",
	Long:  "display the mainnet rules",
	Run: func(_ *cobra.Command, _ []string) {
		terminalWidth, _, err := term.GetSize(int(os.Stdout.Fd()))
		if err != nil {
			terminalWidth = 80
		}
		leftPad := 6
		fmt.Printf("%s\n", markdown.Render(skywire.MainnetRules, terminalWidth, leftPad))
	},
}
