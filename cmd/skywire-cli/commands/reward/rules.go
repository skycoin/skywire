// Package clireward cmd/skywire-cli/reward/rules.go
package clireward

import (
	"bytes"
	"fmt"
	"os"
	"regexp"

	markdown "github.com/MichaelMure/go-term-markdown"
	"github.com/spf13/cobra"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
	"golang.org/x/term"

	"github.com/skycoin/skywire"
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
	Short: "display the mainnet rules",
	Long:  "display the mainnet rules",
	Run: func(_ *cobra.Command, _ []string) {
		if rawFile {
			fmt.Println(skywire.MainnetRules)
			os.Exit(0)
		}
		if asHTML {
			// Preprocess to replace ~text~ with ~~text~~ for strikethrough
			re := regexp.MustCompile(`~(.*?)~`)
			rules := re.ReplaceAllString(skywire.MainnetRules, "~~$1~~")
			var buf bytes.Buffer
			md := goldmark.New(
				goldmark.WithExtensions(extension.Strikethrough),
				goldmark.WithRendererOptions(html.WithXHTML()), // Optional: add XHTML compatibility
			)
			if err := md.Convert([]byte(rules), &buf); err != nil {
				fmt.Println("Error rendering markdown as HTML:", err)
				os.Exit(1)
			}
			fmt.Println(buf.String())
			os.Exit(0)
		}
		terminalWidth, _, err := term.GetSize(int(os.Stdout.Fd()))
		if err != nil {
			terminalWidth = 80
		}
		leftPad := 6
		fmt.Printf("%s\n", markdown.Render(skywire.MainnetRules, terminalWidth, leftPad))
	},
}
