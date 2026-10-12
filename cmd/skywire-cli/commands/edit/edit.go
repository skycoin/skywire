// Package cliedit cmd/skywire-cli/commands/edit/edit.go c4-vis-cli
package cliedit

import (
	"fmt"
	"os"

	"github.com/0magnet/progkit"
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
	"github.com/spf13/cobra"
)

// RootCmd is the edit command
var RootCmd = &cobra.Command{
	Use:    "edit [file]",
	Short:  "Terminal text editor",
	Long:   "Small terminal text editor (Ctrl+S save, Ctrl+Q quit). In the browser shell it edits in a real text area.",
	Hidden: true,
	Args:   cobra.MaximumNArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		filename := "untitled"
		var content string
		if len(args) == 1 {
			filename = args[0]
			data, err := os.ReadFile(filename) //nolint:gosec
			if err != nil && !os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "edit: %v\n", err)
				os.Exit(1)
			}
			content = string(data)
		}
		if err := run(filename, content); err != nil {
			fmt.Fprintf(os.Stderr, "edit: %v\n", err)
			os.Exit(1)
		}
	},
}

var (
	barStyle  = tcell.StyleDefault.Reverse(true)
	hintStyle = tcell.StyleDefault.Foreground(color.PaletteColor(244))
)

func run(filename, content string) error {
	app, err := progkit.Open()
	if err != nil {
		return err
	}
	defer app.Close()

	var dirty, quitArmed bool
	status := ""
	ed := &progkit.Area{ID: "edit", OnChange: func() { dirty = true }}
	ed.SetText(content)

	app.Run(func(f *progkit.Frame) {
		head, body := f.Size().SplitTop(1)
		body, foot := body.SplitBottom(1)
		mark := ""
		if dirty {
			mark = " [modified]"
		}
		l, c := ed.Cursor()
		progkit.Fill(f.Screen, head, barStyle)
		progkit.DrawText(f.Screen, 0, head.Y, head.W, fmt.Sprintf(" %s%s  %d:%d", filename, mark, l+1, c+1), barStyle)
		ed.Draw(f, body, true)
		msg := status
		if msg == "" {
			msg = "Ctrl+S save  Ctrl+Q quit"
		}
		progkit.DrawText(f.Screen, 0, foot.Y, foot.W, msg, hintStyle)
	}, func(ev tcell.Event) bool {
		k, ok := ev.(*tcell.EventKey)
		if !ok {
			return true
		}
		armed := quitArmed
		quitArmed, status = false, ""
		switch {
		case progkit.IsCtrl(k, 'q'):
			if !dirty || armed {
				return false
			}
			quitArmed, status = true, "unsaved changes, Ctrl+Q again to quit"
		case progkit.IsCtrl(k, 's'):
			if err := saveFile(filename, ed.Text()); err != nil {
				status = "save: " + err.Error()
				break
			}
			dirty, status = false, "saved "+filename
		default:
			ed.Key(k)
		}
		return true
	})
	return nil
}

func saveFile(filename, text string) error {
	if filename == "untitled" {
		return fmt.Errorf("no filename specified, use: edit <filename>")
	}
	// 0640, matching visorconfig's own write path: this editor is pointed at the
	// visor config, which holds the SECRET KEY. Writing it back world-readable
	// silently undid that protection.
	return os.WriteFile(filename, []byte(text), 0640) //nolint:gosec
}
