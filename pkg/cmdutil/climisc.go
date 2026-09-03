// Package cmdutil pkg/cmdutil/climisc.go c0-com-util
package cmdutil

import (
	stdjson "encoding/json"
	"log"
	"strings"

	"github.com/spf13/cobra"
)

// RunRoot executes a cobra root command and exits fatally on error. It
// centralizes the identical `func Execute()` wrapper that every command
// package (svc, apps, cli) previously copied verbatim.
func RunRoot(cmd *cobra.Command) {
	if err := cmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}

// ExampleJSON renders v as indented JSON for use in CLI --help example
// blocks. It returns "" on a marshal error. This centralizes the helper
// that every deployment-service command package previously copied.
func ExampleJSON(v interface{}) string {
	b, err := stdjson.MarshalIndent(v, "    ", "  ")
	if err != nil {
		return ""
	}
	return string(b)
}

// CommaSplit splits a comma-separated string into trimmed, non-empty
// fields. It returns nil for an empty input. This centralizes the helper
// that several command packages previously copied.
func CommaSplit(s string) []string {
	if s == "" {
		return nil
	}
	out := make([]string, 0, 4)
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
