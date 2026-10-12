package vt

import (
	"strconv"
	"strings"
)

// OSC 133: semantic prompt marks, the FinalTerm convention that iTerm2,
// kitty, WezTerm and VS Code's terminal all read. A shell that knows about it
// marks where each part of a command's life begins:
//
//	OSC 133 ; A ST          the prompt starts
//	OSC 133 ; B ST          the command line starts (the prompt has ended)
//	OSC 133 ; C ST          the command's output starts
//	OSC 133 ; D [; exit] ST the command has finished, with its exit status
//
// From those the terminal can jump between prompts in the scrollback, and
// hand back what the last command printed. Each mark is a Marker on its
// line, so it scrolls with the text and dies with it: when the line leaves the
// scrollback, when it is erased, when the alternate screen it was on goes.

// commandMark is one command's marks. Any but prompt may be missing.
type commandMark struct {
	buf               *Buffer
	prompt            *Marker // A
	command           *Marker // B
	output            *Marker // C
	end               *Marker // D
	commandCol        int
	outputCol, endCol int
	exit              int
	finished          bool
}

// commandMarksMax is how many commands are remembered; more than a
// scrollback holds, and few enough that looking through them costs nothing.
const commandMarksMax = 1000

func alive(m *Marker) bool { return m != nil && !m.IsDisposed() }

// SemanticPrompt handles OSC 133.
func (h *InputHandler) SemanticPrompt(data string) bool {
	fields := strings.Split(data, ";")
	b := h.activeBuffer
	line, col := b.YBase+b.Y, b.X
	var cur *commandMark
	if n := len(h.commands); n > 0 && h.commands[n-1].buf == b && alive(h.commands[n-1].prompt) {
		cur = h.commands[n-1]
	}
	switch fields[0] {
	case "A":
		// kitty marks a continuation prompt (k=s) the same way, but it is
		// part of the command being typed, not the start of another.
		for _, f := range fields[1:] {
			if f == "k=s" || f == "k=c" {
				return true
			}
		}
		h.pruneCommands()
		h.commands = append(h.commands, &commandMark{buf: b, prompt: b.AddMarker(line), exit: -1})
	case "B":
		if cur != nil && !cur.finished {
			cur.command, cur.commandCol = b.AddMarker(line), col
		}
	case "C":
		if cur != nil && !cur.finished {
			cur.output, cur.outputCol = b.AddMarker(line), col
		}
	case "D":
		if cur != nil && !cur.finished {
			cur.end, cur.endCol = b.AddMarker(line), col
			cur.finished = true
			if len(fields) > 1 {
				if n, err := strconv.Atoi(fields[1]); err == nil {
					cur.exit = n
				}
			}
		}
	}
	return true
}

// pruneCommands forgets commands whose prompt line is gone, and the oldest
// once there are too many.
func (h *InputHandler) pruneCommands() {
	kept := h.commands[:0]
	for _, c := range h.commands {
		if alive(c.prompt) {
			kept = append(kept, c)
		}
	}
	clear(h.commands[len(kept):])
	h.commands = kept
	if over := len(h.commands) - commandMarksMax + 1; over > 0 {
		for _, c := range h.commands[:over] {
			for _, m := range []*Marker{c.prompt, c.command, c.output, c.end} {
				if m != nil {
					m.Dispose()
				}
			}
		}
		h.commands = append(h.commands[:0], h.commands[over:]...)
	}
}

// promptLines are the lines of the prompts on the active screen, in order.
func (h *InputHandler) promptLines() []int {
	var lines []int
	for _, c := range h.commands {
		if c.buf == h.activeBuffer && alive(c.prompt) {
			lines = append(lines, c.prompt.Line)
		}
	}
	return lines
}

// PreviousPromptLine is the line of the last prompt above line, if any.
func (h *InputHandler) PreviousPromptLine(line int) (int, bool) {
	lines := h.promptLines()
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i] < line {
			return lines[i], true
		}
	}
	return 0, false
}

// NextPromptLine is the line of the first prompt below line, if any.
func (h *InputHandler) NextPromptLine(line int) (int, bool) {
	for _, l := range h.promptLines() {
		if l > line {
			return l, true
		}
	}
	return 0, false
}

// LastCommandOutput is what the last finished command printed — from its
// C mark (or, without one, the line after its command line) to its D mark —
// with its exit status, -1 when D did not give one. ok is false when no
// command has finished, or when the lines it printed are gone.
//
// A line that wrapped is joined to the next without a newline, and the
// blanks to the right of a line are dropped, as they are from a selection.
func (h *InputHandler) LastCommandOutput() (text string, exit int, ok bool) {
	var c *commandMark
	for i := len(h.commands) - 1; i >= 0; i-- {
		if h.commands[i].finished {
			c = h.commands[i]
			break
		}
	}
	if c == nil || !alive(c.end) || !alive(c.prompt) {
		return "", 0, false
	}
	var startLine, startCol int
	switch {
	case alive(c.output):
		startLine, startCol = c.output.Line, c.outputCol
	case alive(c.command):
		startLine = c.command.Line + 1
	default:
		startLine = c.prompt.Line + 1
	}
	endLine, endCol := c.end.Line, c.endCol
	if endLine < startLine || (endLine == startLine && endCol < startCol) {
		return "", c.exit, true
	}
	b := c.buf
	cols := h.bufferService.Cols
	var sb strings.Builder
	for l := startLine; l <= endLine && l < b.Lines.Length(); l++ {
		from, to := 0, cols
		if l == startLine {
			from = startCol
		}
		if l == endLine {
			to = endCol
		}
		next := l + 1
		wrapped := next < b.Lines.Length() && b.Lines.Get(next).IsWrapped
		if from < to {
			sb.WriteString(b.Lines.Get(l).TranslateToString(!wrapped || l == endLine, from, to))
		}
		if l < endLine && !wrapped {
			sb.WriteByte('\n')
		}
	}
	return strings.TrimSuffix(sb.String(), "\n"), c.exit, true
}

// resetCommands forgets every mark, at a full reset.
func (h *InputHandler) resetCommands() {
	h.commands = nil
}

// ScrollToPreviousPrompt scrolls the last prompt above the top of the view
// to the top of the view, as kitty's scroll_to_prompt does. It reports
// whether there was one.
func (t *Terminal) ScrollToPreviousPrompt() bool {
	line, ok := t.inputHandler.PreviousPromptLine(t.Buffer().YDisp)
	if ok {
		t.ScrollToLine(line)
	}
	return ok
}

// ScrollToNextPrompt scrolls the first prompt below the top of the view to
// the top of the view, or as near it as the end of the buffer allows. With
// no prompt below, it scrolls to the bottom; it reports whether the view
// moved.
func (t *Terminal) ScrollToNextPrompt() bool {
	b := t.Buffer()
	before := b.YDisp
	if line, ok := t.inputHandler.NextPromptLine(b.YDisp); ok {
		t.ScrollToLine(min(line, b.YBase))
	} else {
		t.ScrollToBottom()
	}
	return t.Buffer().YDisp != before
}

// LastCommandOutput is what the last finished command printed, its exit
// status (-1 if the shell did not say), and whether there is one to give:
// see InputHandler.LastCommandOutput.
func (t *Terminal) LastCommandOutput() (text string, exit int, ok bool) {
	return t.inputHandler.LastCommandOutput()
}
