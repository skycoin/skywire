package charts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBoardSections(t *testing.T) {
	ok := &Page{Title: "Transport discovery", About: "02aa", Build: func(context.Context, Range, time.Time) (Content, error) {
		return Content{Tables: []Table{{Title: "Transports", Head: []string{"type"}, Rows: [][]string{{"dmsg"}}}}}, nil
	}}
	bad := &Page{Title: "Route finder", Build: func(context.Context, Range, time.Time) (Content, error) {
		return Content{}, errors.New("store down")
	}}
	bd := &Board{
		Title: "Deployment",
		Sections: func() []Section {
			return []Section{{Page: ok}, {Page: bad}, {Title: "Setup node", Note: "No charts."}}
		},
		Summary: func() *Table { return &Table{Title: "Services", Head: []string{"service"}, Rows: [][]string{{"tpd"}}} },
	}
	w := httptest.NewRecorder()
	bd.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/?range=7d", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"<h1>Deployment</h1>", ">Services</h2>", "Transport discovery", "02aa", ">dmsg<",
		"Route finder", "Charts unavailable right now.", "Setup node", "No charts.", "aria-current='page'>7d"} {
		if !strings.Contains(body, want) {
			t.Errorf("board lacks %q", want)
		}
	}
	if strings.Index(body, ">Services</h2>") > strings.Index(body, "Transport discovery</h2>") {
		t.Error("summary should come before the sections")
	}
}

// Services that share a process show its charts once, in their own section.
func TestBoardProcessChartsOnce(t *testing.T) {
	page := func(title string) *Page {
		return &Page{Title: title, Build: func(context.Context, Range, time.Time) (Content, error) {
			return Content{Charts: []Chart{
				{Title: "Process CPU", Process: true},
				{Title: title + " requests"},
			}}, nil
		}}
	}
	bd := &Board{Title: "Deployment", Sections: func() []Section {
		return []Section{{Page: page("Transport discovery")}, {Page: page("Route finder")}}
	}}
	w := httptest.NewRecorder()
	bd.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	body := w.Body.String()
	if n := strings.Count(body, "<h2>Process CPU</h2>"); n != 1 {
		t.Fatalf("process chart drawn %d times, want once", n)
	}
	for _, want := range []string{"Transport discovery requests", "Route finder requests", ">Process</h2>"} {
		if !strings.Contains(body, want) {
			t.Errorf("board lacks %q", want)
		}
	}
	if strings.Index(body, ">Process</h2>") > strings.Index(body, ">Transport discovery</h2>") {
		t.Error("the process section should come before the services")
	}
}
