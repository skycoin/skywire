// Package commands cmd/dmsg/dmsg/commands/root_test.go
//
// Covers modifySubcommands, the package's one pure helper. init() wires the
// whole dmsg command tree (run on load), PersistentPreRun installs a signal
// handler that can os.Exit, and Execute is the process entrypoint — none of
// which is meaningfully unit-testable as-is.
package commands

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

// TestModifySubcommands verifies every descendant command (recursively) is
// silenced and stripped of its version, while the root passed in is left
// untouched.
func TestModifySubcommands(t *testing.T) {
	grandchild := &cobra.Command{Use: "gc", Version: "1.0"}
	child := &cobra.Command{Use: "c", Version: "1.0"}
	child.AddCommand(grandchild)
	root := &cobra.Command{Use: "root", Version: "9.9"}
	root.AddCommand(child)

	modifySubcommands(root)

	// The root itself is not modified — only its descendants.
	assert.Equal(t, "9.9", root.Version)
	assert.False(t, root.SilenceErrors)

	for _, c := range []*cobra.Command{child, grandchild} {
		assert.Equal(t, "", c.Version, "%s version should be cleared", c.Use)
		assert.True(t, c.SilenceErrors, "%s SilenceErrors", c.Use)
		assert.True(t, c.SilenceUsage, "%s SilenceUsage", c.Use)
		assert.True(t, c.DisableSuggestions, "%s DisableSuggestions", c.Use)
		assert.True(t, c.DisableFlagsInUseLine, "%s DisableFlagsInUseLine", c.Use)
	}
}

// TestModifySubcommandsNoChildren verifies a leaf command is a safe no-op.
func TestModifySubcommandsNoChildren(t *testing.T) {
	root := &cobra.Command{Use: "root", Version: "9.9"}
	assert.NotPanics(t, func() { modifySubcommands(root) })
	assert.Equal(t, "9.9", root.Version)
}
