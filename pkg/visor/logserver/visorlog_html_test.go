package logserver

import (
	"strings"
	"testing"
)

func TestColorizeLine(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		wantColor string // substring expected in the style attr (empty = no span)
		wantText  string // escaped text expected in output
		wantBold  bool
	}{
		{
			name:      "debug line with module",
			in:        "[2026-06-18T14:54:52-05:00] DEBUG [embedded_tps]: Health check request remote=03dd\n",
			wantColor: levelColors["DEBUG"],
			wantText:  "Health check request remote=03dd",
		},
		{
			name:      "info line",
			in:        "[2026-06-18T14:54:52-05:00] INFO [visor]: Started\n",
			wantColor: levelColors["INFO"],
			wantText:  "Started",
		},
		{
			name:      "error line",
			in:        "[2026-06-18T14:54:52-05:00] ERROR [tp]: boom\n",
			wantColor: levelColors["ERROR"],
			wantText:  "boom",
		},
		{
			name:      "fatal is bold",
			in:        "[2026-06-18T14:54:52-05:00] FATAL [visor]: dying\n",
			wantColor: levelColors["FATAL"],
			wantText:  "dying",
			wantBold:  true,
		},
		{
			name:     "non-standard line passes through escaped, no span",
			in:       "some library output <script>\n",
			wantText: "some library output &lt;script&gt;",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := colorizeLine(tc.in)
			if !strings.Contains(out, tc.wantText) {
				t.Fatalf("output missing text %q: got %q", tc.wantText, out)
			}
			if !strings.HasSuffix(out, "\n") {
				t.Fatalf("trailing newline not preserved: got %q", out)
			}
			if tc.wantColor == "" {
				if strings.Contains(out, "<span") {
					t.Fatalf("expected no span for non-standard line: got %q", out)
				}
				return
			}
			if !strings.Contains(out, "color:"+tc.wantColor) {
				t.Fatalf("expected color %q in output: got %q", tc.wantColor, out)
			}
			if tc.wantBold && !strings.Contains(out, "font-weight:bold") {
				t.Fatalf("expected bold for %s: got %q", tc.name, out)
			}
		})
	}
}

func TestColorizeLineEscapesContent(t *testing.T) {
	// A log line whose message contains HTML must not inject markup.
	in := `[2026-06-18T14:54:52-05:00] INFO [visor]: <img src=x onerror=alert(1)>` + "\n"
	out := colorizeLine(in)
	if strings.Contains(out, "<img") {
		t.Fatalf("unescaped markup leaked into output: %q", out)
	}
	if !strings.Contains(out, "&lt;img") {
		t.Fatalf("expected escaped markup: %q", out)
	}
}
