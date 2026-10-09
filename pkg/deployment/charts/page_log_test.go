package charts

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// A failed render is a 503 to the client and a logged reason on the server.
func TestPageLogsRenderFailure(t *testing.T) {
	var buf bytes.Buffer
	log := logrus.New()
	log.SetOutput(&buf)
	p := &Page{
		Title: "t",
		Store: NewMemoryStore(),
		Log:   log,
		Build: func(context.Context, Range, time.Time) (Content, error) {
			return Content{}, errors.New("redis timeout")
		},
	}
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", rec.Code)
	}
	if !strings.Contains(buf.String(), "redis timeout") {
		t.Fatalf("render error not logged: %q", buf.String())
	}
}
