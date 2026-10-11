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
