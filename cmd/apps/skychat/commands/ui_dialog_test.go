// Package commands cmd/apps/skychat/commands/ui_dialog_test.go
//
// Pins the dialog-overflow contract in the embedded UI's stylesheet.
//
// A dialog on a phone shares the screen with the soft keyboard, which takes
// roughly half of it. Every .modal is laid out at its natural height, so the
// taller ones (Create Group, New Channel — a name field above several
// paragraphs of help text) do not fit in what is left. Two declarations are
// what keep them usable, and both are the kind of line a later tidy-up
// removes as redundant:
//
//   - .modal-overlay must scroll. Without overflow-y the dialog simply
//     overflows a container that cannot be moved.
//   - .modal-overlay must NOT center with align-items, and .modal must carry
//     `margin: auto` instead. A centered flex item that outgrows its container
//     overflows both edges and the part above the top edge is unreachable by
//     scrolling — the reported symptom was the name field being invisible
//     while typing into it. Auto margins center identically when there is
//     room and collapse to 0 when there is not.
//
// Asserting on CSS text is blunt, but the alternative is a headless browser
// for two declarations, and the failure mode this guards is silent: the
// dialog still looks right on a desktop viewport, where nothing overflows.
package commands

import (
	"io"
	"regexp"
	"strings"
	"testing"
)

// cssRule returns the body of the first rule whose selector list is exactly
// sel. Anchored on the newline + indent that starts a top-level rule so a
// nested or compound selector (".modal-overlay.visible .modal") can't match.
func cssRule(t *testing.T, css, sel string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(sel) + `\s*\{([^}]*)\}`)
	m := re.FindStringSubmatch(css)
	if m == nil {
		t.Fatalf("no %q rule in the stylesheet", sel)
	}
	return m[1]
}

func TestModalDialogsScrollUnderTheKeyboard(t *testing.T) {
	f, err := getFileSystem().Open("index.html")
	if err != nil {
		t.Fatalf("open embedded index.html: %v", err)
	}
	defer func() { _ = f.Close() }() //nolint:errcheck
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	css := string(b)

	overlay := cssRule(t, css, ".modal-overlay")
	if !strings.Contains(overlay, "overflow-y: auto") {
		t.Error(".modal-overlay must set overflow-y: auto — it is the scroller for a " +
			"dialog taller than the viewport left by the soft keyboard")
	}
	if strings.Contains(overlay, "align-items: center") {
		t.Error(".modal-overlay must not center with align-items: a centered flex item " +
			"that overflows is clipped at the top with no way to scroll to it; " +
			"centring belongs to .modal's margin: auto")
	}

	modal := cssRule(t, css, ".modal")
	if !strings.Contains(modal, "margin: auto") {
		t.Error(".modal must set margin: auto — it is what centers the dialog now that " +
			".modal-overlay aligns to flex-start")
	}
}
