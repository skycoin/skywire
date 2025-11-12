// Package commands pkg/skywire-utilities/pkg/calvin/cmd/calvin/commands/root.go
package commands

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
)

// RootCmd is the root command
var RootCmd = &cobra.Command{
	Use:   "calvin",
	Short: "generate calvin ascii font from text",
	Long: calvin.AsciiFont("calvin") + `
	generate calvin ascii font from text`,
	RunE: func(_ *cobra.Command, args []string) error {
		var input string

		stat, _ := os.Stdin.Stat() //nolint:errcheck
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			scanner := bufio.NewScanner(os.Stdin)
			var sb strings.Builder
			for scanner.Scan() {
				sb.WriteString(scanner.Text())
				sb.WriteString("\n")
			}
			if err := scanner.Err(); err != nil {
				return fmt.Errorf("error reading stdin: %w", err)
			}
			input = sb.String()
		} else if len(args) > 0 {
			input = strings.Join(args, " ")
		} else {
			return fmt.Errorf("no input provided; pipe text or pass as arguments")
		}

		output := calvin.AsciiFont(input)
		fmt.Println(output)
		return nil
	},
}
