// Package logserver pkg/visor/logserver/visorlog_html.go — terminal-style
// HTML rendering for the /skywire.log (and /visor.log alias) endpoint.
//
// The on-disk log is logrus-formatted plain text (DisableColors:true), e.g.
//
//	[2026-06-18T14:54:52-05:00] DEBUG [tp:02af…]: Serving. remote_pk=… tp_id=…
//
// i.e. `[<timestamp>] LEVEL [module]: message key=val…`. We parse the line
// into its elements and wrap each (HTML-escaped) part in its own colored
// <span> so the page mirrors logrus's console TextFormatter rather than
// flattening to one uniform color:
//
//	timestamp   → dim gray            message → bright/light (the focus)
//	key=         → dimmed             value   → default
//	LEVEL + [module] colored by level:
//	  TRACE → gray   DEBUG → blue/gray   INFO → green   WARN → yellow
//	  ERROR → red    FATAL/PANIC → bold bright-red
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
	"regexp"
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

// Per-element colors mirroring logrus's console TextFormatter: the timestamp
// is dim gray, the message is the brightest part of the line, and the
// key=value fields fade back so the eye lands on the message + level first.
const (
	colorTimestamp = "#808080" // dim gray — `[<iso8601>]`
	colorMessage   = "#e0e0e0" // bright/light — the human-readable message
	colorFieldKey  = "#8a8a8a" // dimmer — `key=` of a structured field
	colorDefault   = "#d0d0d0" // fallback level color
)

// logLineRE splits a standard logrus line into its parts:
//
//	[<timestamp>] LEVEL [module]: <rest>
//
// group 1 = timestamp (incl. brackets), 2 = LEVEL, 3 = module (incl.
// brackets), 4 = the remainder (message + any key=value fields). The
// remainder is optional so a bare `[ts] LEVEL [module]:` still matches.
var logLineRE = regexp.MustCompile(
	`^(\[[^\]]+\])\s+(TRACE|DEBUG|INFO|WARN|WARNING|ERROR|FATAL|PANIC)\s+(\[[^\]]+\]):?\s?(.*)$`)

// fieldRE matches a trailing run of `key=value` structured fields. logrus
// emits these space-separated after the message; values may be quoted.
var fieldRE = regexp.MustCompile(`([A-Za-z0-9_.\-]+)=("[^"]*"|\S*)`)

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

// colorizeLine renders a single (raw, unescaped) log line as terminal-style
// HTML, coloring each element of the standard logrus shape independently so
// the page mirrors the console: dim timestamp, level-colored LEVEL + module,
// a bright message, and faded key=value fields. Every piece of content is
// HTML-escaped, so a log line containing markup can't inject into the page.
//
// Lines that don't match the standard shape (library output, stack frames,
// continuations) are emitted escaped in the default text color.
func colorizeLine(line string) string {
	// Preserve the trailing newline outside the spans so copy-paste keeps
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

	m := logLineRE.FindStringSubmatch(body)
	if m == nil {
		// Non-standard line: escape and emit in the default text color.
		return html.EscapeString(body) + nl
	}
	ts, level, module, rest := m[1], m[2], m[3], m[4]

	levelColor, ok := levelColors[level]
	if !ok {
		levelColor = colorDefault
	}
	bold := ""
	if boldLevels[level] {
		bold = ";font-weight:bold"
	}

	var b strings.Builder
	// Timestamp — dim gray.
	b.WriteString(span(colorTimestamp, "", ts))
	b.WriteByte(' ')
	// LEVEL — level color (bold for FATAL/PANIC).
	b.WriteString(span(levelColor, bold, level))
	b.WriteByte(' ')
	// [module] — level color, keeps the bracketed tag visually tied to
	// the severity it belongs to.
	b.WriteString(span(levelColor, "", module))
	b.WriteString(": ")
	// Remainder: bright message, with any trailing key=value fields faded.
	b.WriteString(colorizeRest(rest))

	return b.String() + nl
}

// colorizeRest renders the message + structured fields portion of a log line.
// The message text is bright; each `key=value` field has a dimmed key and a
// default-colored value. All content is HTML-escaped.
func colorizeRest(rest string) string {
	if rest == "" {
		return ""
	}
	// Find where the trailing key=value run begins (if any). logrus appends
	// fields after the message, so we look for the first field match and treat
	// everything from there on as fields, leaving the message in front of it.
	loc := fieldRE.FindStringIndex(rest)
	if loc == nil {
		// No fields — the whole remainder is the message.
		return span(colorMessage, "", rest)
	}
	msg := rest[:loc[0]]
	fields := rest[loc[0]:]

	var b strings.Builder
	if msg != "" {
		b.WriteString(span(colorMessage, "", msg))
	}
	// Walk each key=value, coloring keys dim and values default. Any
	// separators (spaces) between matches are emitted escaped, default color.
	last := 0
	for _, idx := range fieldRE.FindAllStringSubmatchIndex(fields, -1) {
		if idx[0] > last {
			b.WriteString(html.EscapeString(fields[last:idx[0]]))
		}
		key := fields[idx[2]:idx[3]]
		val := fields[idx[4]:idx[5]]
		b.WriteString(span(colorFieldKey, "", key+"="))
		b.WriteString(span(colorDefault, "", val))
		last = idx[1]
	}
	if last < len(fields) {
		b.WriteString(html.EscapeString(fields[last:]))
	}
	return b.String()
}

// span wraps HTML-escaped text in a colored inline span. extra is appended to
// the style attribute (e.g. ";font-weight:bold") and may be empty.
func span(color, extra, text string) string {
	return fmt.Sprintf(`<span style="color:%s%s">%s</span>`, color, extra, html.EscapeString(text))
}
