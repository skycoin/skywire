//go:build js && wasm

// Package cliedit cmd/skywire-cli/commands/edit/edit_js.go c5-cli-util
//
// Browser (js/wasm) stand-in: femto needs a real terminal. The browser shell
// (websh) ships its own editor, so this stub points there.
package cliedit

import (
	"fmt"

	"github.com/spf13/cobra"
)

// RootCmd is the `util edit` command.
var RootCmd = &cobra.Command{
	Use:    "edit [file]",
	Short:  "Terminal text editor (femto)",
	Hidden: true,
	Args:   cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, _ []string) error {
		return fmt.Errorf("edit: the femto editor is not available in the browser build — use the browser shell's built-in editor")
	},
}
