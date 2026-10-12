// Package commands ui_page_test.go
package commands

import (
	"io"
	"strings"
	"testing"
)

func embeddedPage(t *testing.T) string {
	t.Helper()
	f, err := getFileSystem().Open("index.html")
	if err != nil {
		t.Fatalf("open embedded index.html: %v", err)
	}
	defer func() { _ = f.Close() }() //nolint:errcheck
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	// A Windows checkout may turn the file into CRLF.
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// TestPageStorageWritesAreGuarded pins that every localStorage write is
// inside a try. A full quota throws, and an unguarded write abandoned the
// tap that made it: the voice/video toggle changed mode but kept the old
// icon, and saving the seen list or the tab stopped those taps half done.
func TestPageStorageWritesAreGuarded(t *testing.T) {
	lines := strings.Split(embeddedPage(t), "\n")
	for i, line := range lines {
		if !strings.Contains(line, "localStorage.setItem(") {
			continue
		}
		if strings.Contains(line, "try {") || (i > 0 && strings.TrimSpace(lines[i-1]) == "try {") {
			continue
		}
		t.Errorf("index.html:%d writes localStorage outside a try; use this._store: %s", i+1, strings.TrimSpace(line))
	}
}

// TestRecordingCannotLatch pins the three ways a voice or video take used
// to stay stuck for the life of the page: a getUserMedia that never
// settles, a recorder that throws on start after the take was marked
// active, and a recorder that never fires onstop.
func TestRecordingCannotLatch(t *testing.T) {
	page := embeddedPage(t)
	for _, want := range []string{
		"const watchdog = setInterval(",
		"window.skywirePermissionPrompt",
		"          recorder.start(this._recSliceMs());\n        } catch (err) {",
		"if (!recorder || recorder.state === 'inactive') {",
		"this.rec.stopTimer = setTimeout(",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("index.html lost %q", want)
		}
	}
	start := strings.Index(page, "recorder.start(this._recSliceMs());")
	active := strings.Index(page, "this.rec.active = true;")
	if start < 0 || active < 0 || active < start {
		t.Error("a take must be marked active only after recorder.start returns")
	}
}
