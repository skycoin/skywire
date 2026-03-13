// Package cliedit cmd/skywire-cli/commands/edit/edit.go
package cliedit

import (
	"fmt"
	"os"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/pgavlin/femto"
	"github.com/pgavlin/femto/runtime"
	"github.com/rivo/tview"
	"github.com/spf13/cobra"
)

// RootCmd is the edit command
var RootCmd = &cobra.Command{
	Use:    "edit [file]",
	Short:  "Terminal text editor (femto)",
	Long:   "Embedded terminal text editor with syntax highlighting (Ctrl+S save, Ctrl+Q quit)",
	Hidden: true,
	Args:   cobra.MaximumNArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		var filename string
		var content string

		if len(args) == 1 {
			filename = args[0]
			data, err := os.ReadFile(filename) //nolint:gosec
			if err != nil {
				if !os.IsNotExist(err) {
					fmt.Fprintf(os.Stderr, "edit: %v\n", err)
					os.Exit(1)
				}
				// New file
				content = ""
			} else {
				content = string(data)
			}
		} else {
			filename = "untitled"
			content = ""
		}

		// Create buffer and view
		buffer := femto.NewBufferFromString(content, filename)
		view := femto.NewView(buffer)

		// Load syntax highlighting colorscheme
		if cs := loadColorscheme(); cs != nil {
			view.SetColorscheme(cs)
		}

		// Set up tview application
		app := tview.NewApplication()
		view.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
			switch {
			case event.Key() == tcell.KeyCtrlQ:
				app.Stop()
				return nil
			case event.Key() == tcell.KeyCtrlS:
				if err := saveFile(filename, view); err != nil {
					fmt.Fprintf(os.Stderr, "\nedit: save error: %v\n", err)
				}
				return nil
			}
			return event
		})

		app.SetRoot(view, true)
		if err := app.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "edit: %v\n", err)
			os.Exit(1)
		}
	},
}

func saveFile(filename string, view *femto.View) error {
	if filename == "untitled" {
		return fmt.Errorf("no filename specified, use: edit <filename>")
	}
	return os.WriteFile(filename, []byte(view.Buf.String()), 0644) //nolint:gosec
}

func loadColorscheme() femto.Colorscheme {
	for _, f := range runtime.Files.ListRuntimeFiles(femto.RTColorscheme) {
		if strings.Contains(f.Name(), "monokai") {
			data, err := f.Data()
			if err != nil {
				continue
			}
			return femto.ParseColorscheme(string(data))
		}
	}
	return nil
}
