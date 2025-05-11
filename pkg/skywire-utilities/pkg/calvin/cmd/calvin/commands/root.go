// package commands cmd/calvin/commands/root.go
package commands

import (
	"bufio"
	"strings"
	"fmt"
	"os"
	"github.com/spf13/cobra"
	"github.com/0magnet/calvin"

)

// rootCmd represents the base command for the application
var RootCmd = &cobra.Command{
	Use:   "calvin",
	Short: "generate calvin ascii font from text",
	Long: ``,
	RunE: func(cmd *cobra.Command, args []string) error {
		var input string

		// Check if stdin has input
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			// Reading from stdin
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
			// Reading from command-line arguments
			input = strings.Join(args, " ")
		} else {
			// No input provided
			return fmt.Errorf("no input provided; pipe text or pass as arguments")
		}

		// Generate and print the ASCII font
		output := calvin.AsciiFont(input)
		fmt.Println(output)
		return nil
	},
}
