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
	// Separate keeps each section's process charts in it, for sections that
	// are separate processes, such as the visors of a hypervisor.
	Separate bool

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
	built := make([]builtSection, len(sections))
	var process builtSection
	for i, s := range sections {
		built[i] = bd.build(ctx, s, rg, now)
		if bd.Separate {
			continue
		}
		var own, rest []Chart
		for _, c := range built[i].content.Charts {
			if c.Process {
				own = append(own, c)
			} else {
				rest = append(rest, c)
			}
		}
		built[i].content.Charts = rest
		if process.content.Charts == nil && own != nil {
			process = builtSection{title: "Process", about: "Every service on this page runs in this one process.",
				starts: built[i].starts, content: Content{Charts: own}}
		}
	}
	if process.content.Charts != nil {
		process.write(&b, "process", "pc")
	}
	for i, s := range built {
		s.write(&b, fmt.Sprintf("s%d", i), fmt.Sprintf("s%dc", i))
	}
	writeFoot(&b, now)
	if bd.cache == nil {
		bd.cache = map[string]cached{}
	}
	c := newCached(b.Bytes())
	bd.cache[rg.Name] = c
	return c, nil
}

// builtSection is one section of a board, rendered but not yet written.
type builtSection struct {
	title, about, note string
	links              []Link
	starts             []Start
	content            Content
}

// build renders one service. A page that fails to render is noted in its
// section rather than failing the board.
func (bd *Board) build(ctx context.Context, s Section, rg Range, now time.Time) builtSection {
	out := builtSection{title: s.Title, about: s.About, note: s.Note}
	if out.title == "" && s.Page != nil {
		out.title = s.Page.Title
	}
	if out.about == "" && s.Page != nil {
		out.about = s.Page.About
	}
	if s.Page != nil {
		out.links = s.Page.Links
		var err error
		if out.content, out.starts, err = s.Page.content(ctx, rg, now); err != nil {
			if bd.Log != nil {
				bd.Log.WithError(err).WithField("service", out.title).Warn("charts section render failed")
			}
			out.note = "Charts unavailable right now."
		}
	}
	return out
}

func (s builtSection) write(b *bytes.Buffer, id, chartPrefix string) {
	fmt.Fprintf(b, "<section class='svc' id='%s'><h2 class='svc'>%s</h2>", id, html.EscapeString(s.title))
	writeIntro(b, s.about, s.starts, s.links)
	if s.note != "" {
		fmt.Fprintf(b, "<p class='note'>%s</p>", html.EscapeString(s.note))
	}
	writeContent(b, s.content, chartPrefix)
	b.WriteString("</section>")
}

// Extras serves the Extra pages of the board's sections at their paths and
// 404 for any other path. Mount it at "/" beside the board.
func (bd *Board) Extras() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if bd.Sections != nil {
			for _, s := range bd.Sections() {
				if s.Page == nil {
					continue
				}
				for _, e := range s.Page.Extra {
					if e.Path == r.URL.Path {
						e.Handler.ServeHTTP(w, r)
						return
					}
				}
			}
		}
		http.NotFound(w, r)
	})
}
