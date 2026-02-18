// Package clijq cmd/skywire-cli/commands/jq/jq.go
package clijq

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/itchyny/gojq"
	"github.com/spf13/cobra"
)

var (
	rawOutput    bool
	nullInput    bool
	slurpInput   bool
	compactOutput bool
)

func init() {
	RootCmd.Flags().BoolVarP(&rawOutput, "raw-output", "r", false, "output raw strings without JSON quotes")
	RootCmd.Flags().BoolVarP(&nullInput, "null-input", "n", false, "use null as input instead of reading from stdin")
	RootCmd.Flags().BoolVarP(&slurpInput, "slurp", "s", false, "read all inputs into an array")
	RootCmd.Flags().BoolVarP(&compactOutput, "compact-output", "c", false, "compact output (no pretty printing)")
}

// RootCmd is the jq command
var RootCmd = &cobra.Command{
	Use:    "jq <filter> [file...]",
	Short:  "jq-like JSON processor (gojq)",
	Long:   "Process JSON using jq filter syntax (powered by gojq)",
	Hidden: true,
	Args:   cobra.MinimumNArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		filter := args[0]
		files := args[1:]

		// Parse the query
		query, err := gojq.Parse(filter)
		if err != nil {
			fmt.Fprintf(os.Stderr, "jq: invalid query: %v\n", err)
			os.Exit(1)
		}

		// Compile the query
		code, err := gojq.Compile(query)
		if err != nil {
			fmt.Fprintf(os.Stderr, "jq: compile error: %v\n", err)
			os.Exit(1)
		}

		// Get inputs
		var inputs []any
		if nullInput {
			inputs = []any{nil}
		} else if len(files) > 0 {
			for _, file := range files {
				fileInputs, err := readJSONFile(file)
				if err != nil {
					fmt.Fprintf(os.Stderr, "jq: %v\n", err)
					os.Exit(1)
				}
				inputs = append(inputs, fileInputs...)
			}
		} else {
			stdinInputs, err := readJSONFromReader(os.Stdin)
			if err != nil {
				fmt.Fprintf(os.Stderr, "jq: %v\n", err)
				os.Exit(1)
			}
			inputs = stdinInputs
		}

		// Handle slurp mode
		if slurpInput && len(inputs) > 0 {
			inputs = []any{inputs}
		}

		// Process each input
		hasError := false
		for _, input := range inputs {
			iter := code.Run(input)
			for {
				v, ok := iter.Next()
				if !ok {
					break
				}
				if err, ok := v.(error); ok {
					if haltErr, ok := err.(*gojq.HaltError); ok && haltErr.Value() == nil {
						break
					}
					fmt.Fprintf(os.Stderr, "jq: %v\n", err)
					hasError = true
					continue
				}
				if err := outputValue(v); err != nil {
					fmt.Fprintf(os.Stderr, "jq: %v\n", err)
					hasError = true
				}
			}
		}

		if hasError {
			os.Exit(1)
		}
	},
}

func readJSONFile(filename string) ([]any, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck
	return readJSONFromReader(f)
}

func readJSONFromReader(r io.Reader) ([]any, error) {
	var inputs []any
	decoder := json.NewDecoder(bufio.NewReader(r))
	for {
		var v any
		if err := decoder.Decode(&v); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		inputs = append(inputs, v)
	}
	return inputs, nil
}

func outputValue(v any) error {
	if rawOutput {
		if s, ok := v.(string); ok {
			fmt.Println(s)
			return nil
		}
	}

	var output []byte
	var err error
	if compactOutput {
		output, err = json.Marshal(v)
	} else {
		output, err = json.MarshalIndent(v, "", "  ")
	}
	if err != nil {
		return err
	}

	// Remove trailing newline if present in compact mode
	s := string(output)
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	fmt.Print(s)
	return nil
}
