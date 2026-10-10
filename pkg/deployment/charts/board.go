package charts

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"net/http"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// Board serves the charts of several services as one page, a section each,
// such as every service a deployment host runs.
type Board struct {
	Title string
	About string
	// Sections is read on every render, so it can follow services that
	// start and stop.
	Sections func() []Section
	// Summary, when set, is a table shown before the sections.
	Summary func() *Table
	Log     logrus.FieldLogger

	mu    sync.Mutex
	cache map[string]cached
}

// Section is one service on a Board.
type Section struct {
	Title string
	About string
	// Page is the service's charts. Nil shows the title, About and Note only.
	Page *Page
	// Note is shown under the title, such as why there are no charts.
	Note string
}

// ServeHTTP renders the board for ?range=, from a copy at most a minute old.
func (bd *Board) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	serveRendered(w, r, bd.render, bd.Log)
}

func (bd *Board) render(ctx context.Context, rg Range) (cached, error) {
	bd.mu.Lock()
	defer bd.mu.Unlock()
	if c, ok := bd.cache[rg.Name]; ok && time.Since(c.at) < cacheFor {
		return c, nil
	}
	now := time.Now().UTC()
	var b bytes.Buffer
	writeHead(&b, bd.Title)
	writeIntro(&b, bd.About, nil, nil)
	writeNav(&b, rg)
	if bd.Summary != nil {
		if t := bd.Summary(); t != nil {
			writeTable(&b, *t)
		}
	}
	var sections []Section
	if bd.Sections != nil {
		sections = bd.Sections()
	}
	for i, s := range sections {
		bd.writeSection(ctx, &b, i, s, rg, now)
	}
	writeFoot(&b, now)
	if bd.cache == nil {
		bd.cache = map[string]cached{}
	}
	c := newCached(b.Bytes())
	bd.cache[rg.Name] = c
	return c, nil
}

// writeSection writes one service. A page that fails to render is noted in
// its section rather than failing the board.
func (bd *Board) writeSection(ctx context.Context, b *bytes.Buffer, i int, s Section, rg Range, now time.Time) {
	title := s.Title
	if title == "" && s.Page != nil {
		title = s.Page.Title
	}
	fmt.Fprintf(b, "<section class='svc' id='s%d'><h2 class='svc'>%s</h2>", i, html.EscapeString(title))
	note := s.Note
	var content Content
	var starts []Start
	if s.Page != nil {
		var err error
		if content, starts, err = s.Page.content(ctx, rg, now); err != nil {
			if bd.Log != nil {
				bd.Log.WithError(err).WithField("service", title).Warn("charts section render failed")
			}
			note = "Charts unavailable right now."
		}
	}
	about := s.About
	if about == "" && s.Page != nil {
		about = s.Page.About
	}
	writeIntro(b, about, starts, nil)
	if note != "" {
		fmt.Fprintf(b, "<p class='note'>%s</p>", html.EscapeString(note))
	}
	writeContent(b, content, fmt.Sprintf("s%dc", i))
	b.WriteString("</section>")
}
