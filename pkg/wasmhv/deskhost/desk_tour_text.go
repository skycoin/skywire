// Package deskhost pkg/wasmhv/deskhost/desk_tour_text.go c4-wasm-desk
package deskhost

import (
	_ "embed" // the tour's words, desk-tour.md
	"fmt"
	"html"
	"regexp"
	"strings"
)

// The desk tour's words live in desk-tour.md so they can be edited without
// touching code. This file reads them; desk_tour_js.go shows them.
//
//go:embed desk-tour.md
var deskTourMD string

// tourApps is the wiring: the desk app each step opens and its arguments, by
// the step's id in desk-tour.md. Every step is listed, nil where the step opens
// nothing, so a mistyped id in either file is caught by
// TestDeskTourTextMatchesWiring. The launcher step opens the taskbar menu
// instead, in desk_tour_js.go.
var tourApps = map[string][]string{
	"intro":     nil,
	"launcher":  nil,
	"browser":   {"browser", "https://ip.skycoin.com/"},
	"dashboard": {"browser", dashboardURL},
	"console":   {"console"},
	"files":     {"files"},
	"mail":      {"browser", mailURL},
	"identity":  {"identity"},
	"pair":      {"pair"},
	"settings":  {"settings"},
	"install":   nil, // its app is the browser's install prompt, which Next must not fire
	"close":     nil,
}

// The dashboard's addresses on the virtual loopback. embed=1 in the query tells
// the hypervisor the page is framed, and in the hash it drops the tab chrome.
const (
	dashboardURL = "http://vnet:8001/?embed=1#/?embed=1"
	mailURL      = "http://vnet:8001/?embed=1#/nodes/local/mail?embed=1"
)

// tourText is one step's words, rendered to the small HTML vocabulary the desk
// panes use.
type tourText struct {
	id    string
	title string
	body  string
}

var (
	tourStepRE  = regexp.MustCompile(`^## (\S+)\s*$`)
	tourFieldRE = regexp.MustCompile(`^(Title|Body):\s*(.*)$`)
	mdCodeRE    = regexp.MustCompile("`([^`]+)`")
	mdBoldRE    = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	mdItalicRE  = regexp.MustCompile(`\*([^*]+)\*`)
)

// parseDeskTour reads desk-tour.md. Anything before the first "## " is the
// file's own notes. A step without a title or a body is an error.
func parseDeskTour(md string) ([]tourText, error) {
	var steps []tourText
	var field string
	var lines []string
	closeField := func() {
		if len(steps) == 0 || field == "" {
			return
		}
		s := &steps[len(steps)-1]
		text := strings.Join(lines, "\n")
		switch field {
		case "Title":
			s.title = mdInline(strings.TrimSpace(text))
		case "Body":
			s.body = mdBlock(text)
		}
		field, lines = "", nil
	}
	for _, line := range strings.Split(md, "\n") {
		if m := tourStepRE.FindStringSubmatch(line); m != nil {
			closeField()
			steps = append(steps, tourText{id: m[1]})
			continue
		}
		if len(steps) == 0 {
			continue
		}
		if m := tourFieldRE.FindStringSubmatch(line); m != nil {
			closeField()
			field = m[1]
			if m[2] != "" {
				lines = []string{m[2]}
			}
			continue
		}
		if field != "" {
			lines = append(lines, line)
		}
	}
	closeField()
	for _, s := range steps {
		if s.title == "" || s.body == "" {
			return nil, fmt.Errorf("desk-tour.md: step %q needs both a Title and a Body", s.id)
		}
	}
	return steps, nil
}

// mdInline renders `code`, **bold** and *italic*. Code spans are escaped and
// set aside first so their contents are left alone.
func mdInline(s string) string {
	var codes []string
	s = mdCodeRE.ReplaceAllStringFunc(s, func(m string) string {
		codes = append(codes, "<code>"+html.EscapeString(m[1:len(m)-1])+"</code>")
		return fmt.Sprintf("\x00%d\x00", len(codes)-1)
	})
	s = mdBoldRE.ReplaceAllString(s, "<b>$1</b>")
	s = mdItalicRE.ReplaceAllString(s, "<i>$1</i>")
	for i, c := range codes {
		s = strings.Replace(s, fmt.Sprintf("\x00%d\x00", i), c, 1)
	}
	return s
}

// mdBlock renders a field: blank lines separate paragraphs, and a run of lines
// starting with "- " is a list. Lines inside a paragraph join with a space.
func mdBlock(text string) string {
	var out strings.Builder
	var para, items []string
	prev := ""
	flush := func() {
		if len(para) > 0 {
			if prev == "p" {
				out.WriteString("<br><br>")
			}
			out.WriteString(mdInline(strings.Join(para, " ")))
			para, prev = nil, "p"
		}
		if len(items) > 0 {
			out.WriteString("<ul>")
			for _, it := range items {
				out.WriteString("<li>" + mdInline(it) + "</li>")
			}
			out.WriteString("</ul>")
			items, prev = nil, "ul"
		}
	}
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == "":
			flush()
		case strings.HasPrefix(t, "- "):
			if len(para) > 0 {
				flush()
			}
			items = append(items, t[2:])
		default:
			if len(items) > 0 {
				flush()
			}
			para = append(para, t)
		}
	}
	flush()
	return out.String()
}
