// Package logserver pkg/visor/logserver/visorlog_html.go — terminal-style
// HTML rendering for the /skywire.log (and /visor.log alias) endpoint.
//
// The on-disk log is logrus-formatted plain text (DisableColors:true), e.g.
//
//	[2026-06-18T14:54:52-05:00] DEBUG [tp:02af…]: Serving. remote_pk=… tp_id=…
//
// i.e. `[<timestamp>] LEVEL [module]: message key=val…`. We parse the LEVEL
// token per line and wrap the (HTML-escaped) line in a colored <span> whose
// color mirrors logrus's console color scheme:
//
//	TRACE → gray   DEBUG → blue/gray   INFO → green   WARN → yellow
//	ERROR → red    FATAL/PANIC → bold bright-red
//
// Everything is inline CSS/HTML — no external assets, no JS, dependency-free.
// All log content is HTML-escaped before being written, so a log line that
// happens to contain markup can't inject anything into the page.
//
// A plaintext escape hatch (?raw=1) bypasses the HTML wrapper entirely so
// log-scraping tooling keeps receiving the file verbatim.
package logserver

import (
	"bufio"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// levelColors maps a logrus level token (as it appears upper-cased in the
// on-disk log) to an inline CSS color. The palette tracks logrus's default
// console ColorScheme: info=green, warn=yellow, error/fatal/panic=red,
// debug=blue, trace=black(dimmed). Tuned slightly brighter for legibility
// on the near-black terminal background.
var levelColors = map[string]string{
	"TRACE":   "#808080",
	"DEBUG":   "#5f87ff",
	"INFO":    "#5fd75f",
	"WARN":    "#d7d75f",
	"WARNING": "#d7d75f",
	"ERROR":   "#ff5f5f",
	"FATAL":   "#ff0000",
	"PANIC":   "#ff0000",
}

// boldLevels render with extra weight — these are the ones you don't want to
// scroll past.
var boldLevels = map[string]bool{
	"FATAL": true,
	"PANIC": true,
}

// renderVisorLogHTML streams logFile to the client as a terminal-styled HTML
// page: near-black background, light monospace text, one colored line per log
// entry keyed off its parsed level. Content is HTML-escaped per line.
//
// It streams rather than buffering so a multi-megabyte log doesn't balloon
// memory, flushing periodically for responsiveness over dmsg/skynet.
func renderVisorLogHTML(c *gin.Context, logFile string) {
	f, err := os.Open(logFile) //nolint:gosec // path comes from localPath config, not request
	if err != nil {
		c.String(http.StatusInternalServerError, "open skywire.log: %v", err)
		return
	}
	defer f.Close() //nolint:errcheck

	c.Writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	c.Writer.WriteHeader(http.StatusOK)

	// Page head + terminal styling. <pre> preserves the log's own spacing;
	// per-line <span>s carry the level color.
	//nolint:errcheck // .golangci.yml has check-blank:true so the _,_ discard alone is not enough; nolint silences errcheck while the discard satisfies gosec G104
	_, _ = io.WriteString(c.Writer,
		`<!doctype html><html><head><meta charset="utf-8"><title>skywire.log</title>`+
			`<style>`+
			`body{background:#0a0a0a;color:#d0d0d0;font-family:'DejaVu Sans Mono',Menlo,Consolas,monospace;`+
			`font-size:13px;line-height:1.35;margin:0;padding:12px}`+
			`pre{margin:0;white-space:pre-wrap;word-break:break-word}`+
			`.ts{color:#6c6c6c}`+
			`</style></head><body><pre>`)

	r := bufio.NewReaderSize(f, 64*1024)
	ctx := c.Request.Context()
	flushEvery := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line, rerr := r.ReadString('\n')
		if len(line) > 0 {
			if _, werr := io.WriteString(c.Writer, colorizeLine(line)); werr != nil {
				return
			}
			flushEvery++
			if flushEvery%256 == 0 {
				c.Writer.Flush()
			}
		}
		if rerr != nil {
			break
		}
	}
	_, _ = io.WriteString(c.Writer, "</pre></body></html>") //nolint:errcheck
	c.Writer.Flush()
}

// colorizeLine wraps a single (raw, unescaped) log line in a colored span
// based on its parsed level, HTML-escaping the content. Lines that don't
// match the standard logrus shape are emitted escaped in the default color.
func colorizeLine(line string) string {
	// Preserve the trailing newline outside the span so copy-paste keeps
	// line breaks; strip it for parsing.
	nl := ""
	body := line
	if strings.HasSuffix(body, "\n") {
		nl = "\n"
		body = strings.TrimSuffix(body, "\n")
	}
	if body == "" {
		return nl
	}

	m := logLevelRE.FindStringSubmatch(body)
	if m == nil {
		// Non-standard line (library output, stack frame, continuation):
		// escape and emit in the default text color.
		return html.EscapeString(body) + nl
	}
	color, ok := levelColors[m[1]]
	if !ok {
		color = "#d0d0d0"
	}
	style := "color:" + color
	if boldLevels[m[1]] {
		style += ";font-weight:bold"
	}
	return fmt.Sprintf(`<span style="%s">%s</span>%s`, style, html.EscapeString(body), nl)
}
