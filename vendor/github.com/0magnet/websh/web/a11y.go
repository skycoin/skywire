//go:build js && wasm

package web

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"syscall/js"

	"github.com/0magnet/sh/v3/interp"

	"github.com/0magnet/websh/shell"
)

// Accessibility and shell integration.
//
// Screen reader mode (xterm-go's port of xterm.js's) keeps the screen's text
// in a list a screen reader reads and announces new output. It costs a
// little on every frame, so it is off until asked for: a button that is the
// first thing a screen reader meets on the page, visible only once focused,
// turns it on, as does the a11y command. The choice is kept in this browser.
//
// The shell marks its prompts and commands (OSC 133, as bash, zsh and fish
// do with shell integration), so the terminal knows where each command's
// output is: Ctrl+Shift+Up and Down move between prompts, and Ctrl+Shift+O
// copies the last command's output.

const a11yKey = "websh.screenReader"

// wireA11y adds the screen reader switch and the prompt keys.
func (s *Session) wireA11y(el, box js.Value) {
	doc := js.Global().Get("document")
	btn := doc.Call("createElement", "button")
	btn.Set("className", "websh-a11y")
	btn.Get("style").Set("cssText", "position:absolute;left:8px;top:8px;z-index:40;padding:6px 10px;font:14px sans-serif;"+
		"background:#1d4ed8;color:#fff;border:0;border-radius:4px;clip-path:inset(50%);width:1px;height:1px;overflow:hidden")
	show := js.FuncOf(func(js.Value, []js.Value) any {
		btn.Get("style").Set("clipPath", "none")
		btn.Get("style").Set("width", "auto")
		btn.Get("style").Set("height", "auto")
		return nil
	})
	hide := js.FuncOf(func(js.Value, []js.Value) any {
		btn.Get("style").Set("clipPath", "inset(50%)")
		btn.Get("style").Set("width", "1px")
		btn.Get("style").Set("height", "1px")
		return nil
	})
	btn.Call("addEventListener", "focus", show)
	btn.Call("addEventListener", "blur", hide)
	label := func() {
		if s.Term.ScreenReaderMode() {
			btn.Set("textContent", "Turn off screen reader mode")
		} else {
			btn.Set("textContent", "Turn on screen reader mode")
		}
	}
	s.setScreenReader = func(on bool) {
		s.Term.SetScreenReaderMode(on)
		label()
		storeSet(a11yKey, map[bool]string{true: "1", false: ""}[on])
	}
	btn.Call("addEventListener", "click", js.FuncOf(func(js.Value, []js.Value) any {
		s.setScreenReader(!s.Term.ScreenReaderMode())
		if ta := box.Call("querySelector", "textarea"); ta.Truthy() {
			ta.Call("focus")
		}
		return nil
	}))
	el.Call("prepend", btn)
	if storeGet(a11yKey) == "1" {
		s.Term.SetScreenReaderMode(true)
	}
	label()

	// The prompt keys, ahead of the terminal's own handling.
	box.Call("addEventListener", "keydown", js.FuncOf(func(_ js.Value, args []js.Value) any {
		e := args[0]
		if !e.Get("ctrlKey").Bool() || !e.Get("shiftKey").Bool() || e.Get("altKey").Bool() {
			return nil
		}
		switch e.Get("key").String() {
		case "ArrowUp":
			s.Term.ScrollToPreviousPrompt()
		case "ArrowDown":
			s.Term.ScrollToNextPrompt()
		case "O", "o":
			s.copyLastOutput()
		default:
			return nil
		}
		e.Call("preventDefault")
		e.Call("stopPropagation")
		return nil
	}), true)
}

// copyLastOutput puts the last finished command's output on the clipboard.
func (s *Session) copyLastOutput() {
	text, exit, ok := s.Term.LastCommandOutput()
	if !ok {
		s.toast("Nothing to copy", "no command has finished on this screen")
		return
	}
	cb := js.Global().Get("navigator").Get("clipboard")
	if !cb.Truthy() {
		return
	}
	cb.Call("writeText", text)
	note := "1 line"
	if lines := strings.Count(text, "\n") + 1; lines > 1 {
		note = strconv.Itoa(lines) + " lines"
	}
	if exit > 0 {
		note += ", exit status " + strconv.Itoa(exit)
	}
	s.toast("Copied the last command's output", note)
}

// osc133 is the shell's own mark: A before a prompt, B after it, C as a
// command starts, D;status as it ends.
func (s *Session) osc133(mark string) { s.Term.WriteString("\x1b]133;" + mark + "\x1b\\") }

// exitCode is a command's exit status from what Run returned.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var es interp.ExitStatus
	if errors.As(err, &es) {
		return int(es)
	}
	return 1
}

// storeGet and storeSet keep a viewer's preference in this browser, where
// it can be kept; private windows and blocked storage forget it.
func storeGet(key string) (v string) {
	defer func() { _ = recover() }() //nolint:errcheck // storage that throws is storage there is not
	ls := js.Global().Get("localStorage")
	if !ls.Truthy() {
		return ""
	}
	if r := ls.Call("getItem", key); r.Type() == js.TypeString {
		return r.String()
	}
	return ""
}

func storeSet(key, v string) {
	defer func() { _ = recover() }() //nolint:errcheck // as storeGet
	ls := js.Global().Get("localStorage")
	if !ls.Truthy() {
		return
	}
	if v == "" {
		ls.Call("removeItem", key)
	} else {
		ls.Call("setItem", key, v)
	}
}

func init() {
	shell.RegisterApplet("a11y", "screen reader mode: a11y [on|off]", func(_ context.Context, sh *shell.Shell, hc *interp.HandlerContext, args []string) int {
		s := SessionFor(sh)
		if s == nil || s.setScreenReader == nil {
			shell.Printf(hc.Stderr, "a11y: no terminal here\n")
			return 1
		}
		switch {
		case len(args) > 0 && args[0] == "on":
			s.setScreenReader(true)
		case len(args) > 0 && args[0] == "off":
			s.setScreenReader(false)
		case len(args) > 0:
			shell.Printf(hc.Stderr, "usage: a11y [on|off]\n")
			return 2
		}
		state := "off"
		if s.Term.ScreenReaderMode() {
			state = "on"
		}
		shell.Printf(hc.Stdout, "screen reader mode is %s\n", state)
		return 0
	})
}
