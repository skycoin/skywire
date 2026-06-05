package logserver

import (
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestPassesFilters covers the per-line predicate independently of the
// streaming machinery. Each case lists the level/module filters and
// asserts the expected verdict on a representative line shape.
func TestPassesFilters(t *testing.T) {
	cases := []struct {
		name        string
		line        string
		minLevel    string // "" = no level filter
		moduleRe    string // "" = no module filter
		grepRe      string // "" = no grep filter
		strictLevel bool
		wantPass    bool
	}{
		{
			name:     "no filters keeps everything",
			line:     "[2026-06-05T13:36:41Z] DEBUG [tp:abc]: Serving",
			wantPass: true,
		},
		{
			name:     "min-level INFO drops DEBUG",
			line:     "[2026-06-05T13:36:41Z] DEBUG [tp:abc]: Serving",
			minLevel: "INFO", strictLevel: true,
			wantPass: false,
		},
		{
			name:     "min-level INFO keeps WARN",
			line:     "[2026-06-05T13:36:41Z] WARN [tp:abc]: stale conn",
			minLevel: "INFO", strictLevel: true,
			wantPass: true,
		},
		{
			name:     "module regex matches",
			line:     "[2026-06-05T13:36:41Z] DEBUG [sudph]: bound 56194",
			moduleRe: "sudph",
			wantPass: true,
		},
		{
			name:     "module regex misses",
			line:     "[2026-06-05T13:36:41Z] DEBUG [stcp]: bound :7777",
			moduleRe: "sudph",
			wantPass: false,
		},
		{
			name:     "module regex with anchor matches prefix",
			line:     "[2026-06-05T13:36:41Z] DEBUG [tp:abc]: Serving",
			moduleRe: "^tp:",
			wantPass: true,
		},
		{
			name:     "strict level drops unparseable line",
			line:     "free-form line from some library without standard format",
			minLevel: "INFO", strictLevel: true,
			wantPass: false,
		},
		{
			name:     "module filter drops unparseable line",
			line:     "free-form line from some library without standard format",
			moduleRe: "sudph",
			wantPass: false,
		},
		{
			name:     "WARNING aliases to WARN rank",
			line:     "[2026-06-05T13:36:41Z] WARNING [tp:abc]: stale",
			minLevel: "WARN", strictLevel: true,
			wantPass: true,
		},
		{
			name:     "grep regex on full line",
			line:     "[2026-06-05T13:36:41Z] DEBUG [tp:abc]: Serving remote_pk=abc",
			grepRe:   "remote_pk=abc",
			wantPass: true,
		},
		{
			name:     "grep regex misses",
			line:     "[2026-06-05T13:36:41Z] DEBUG [tp:abc]: Serving",
			grepRe:   "remote_pk=xyz",
			wantPass: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var modRE, grepRE *regexp.Regexp
			if tc.moduleRe != "" {
				modRE = regexp.MustCompile(tc.moduleRe)
			}
			if tc.grepRe != "" {
				grepRE = regexp.MustCompile(tc.grepRe)
			}
			minRank, hasMinLevel := levelOrder[tc.minLevel]
			got := passesFilters(tc.line, minRank, hasMinLevel, modRE, grepRE, tc.strictLevel)
			if got != tc.wantPass {
				t.Fatalf("passesFilters(%q, level=%q, mod=%q, grep=%q) = %v, want %v",
					tc.line, tc.minLevel, tc.moduleRe, tc.grepRe, got, tc.wantPass)
			}
		})
	}
}

// TestStreamFilteredVisorLog drives the gin handler end-to-end with a
// tempfile log + query params, then asserts what comes back over the
// response body matches the expected filtered subset. Covers the
// common operator-facing combos.
func TestStreamFilteredVisorLog(t *testing.T) {
	tmp, err := os.MkdirTemp("", "logserver-filter-")
	if err != nil {
		t.Fatalf("mktemp: %v", err)
	}
	defer os.RemoveAll(tmp) //nolint:errcheck

	logFile := filepath.Join(tmp, "visor.log")
	body := strings.Join([]string{
		"[2026-06-05T13:36:41Z] DEBUG [tp:abc]: Serving",
		"[2026-06-05T13:36:42Z] INFO [sudph]: bound port 56194",
		"[2026-06-05T13:36:43Z] WARN [tp:abc]: stale conn",
		"[2026-06-05T13:36:44Z] ERROR [sudph]: handshake timeout",
		"free-form unstructured line",
		"[2026-06-05T13:36:45Z] INFO [stcp]: listening :7777",
	}, "\n") + "\n"
	if err := os.WriteFile(logFile, []byte(body), 0o644); err != nil { //nolint:gosec
		t.Fatalf("write log: %v", err)
	}

	cases := []struct {
		name        string
		query       url.Values
		wantContain []string
		wantOmit    []string
	}{
		{
			name:        "min-level WARN drops DEBUG/INFO + unparseable",
			query:       url.Values{"min-level": []string{"WARN"}},
			wantContain: []string{"WARN [tp:abc]", "ERROR [sudph]"},
			wantOmit:    []string{"DEBUG [tp:abc]", "INFO [sudph]", "INFO [stcp]", "free-form"},
		},
		{
			name:        "module sudph keeps only sudph lines",
			query:       url.Values{"module": []string{"sudph"}},
			wantContain: []string{"INFO [sudph]: bound", "ERROR [sudph]: handshake"},
			wantOmit:    []string{"[tp:abc]", "[stcp]", "free-form"},
		},
		{
			name:        "limit caps output",
			query:       url.Values{"min-level": []string{"DEBUG"}, "limit": []string{"2"}},
			wantContain: []string{"DEBUG [tp:abc]: Serving", "INFO [sudph]: bound"},
			wantOmit:    []string{"WARN [tp:abc]", "ERROR [sudph]"},
		},
		{
			name:        "grep on body text",
			query:       url.Values{"grep": []string{"port 56194"}},
			wantContain: []string{"INFO [sudph]: bound port 56194"},
			wantOmit:    []string{"DEBUG [tp:abc]", "WARN", "ERROR", "stcp"},
		},
		{
			name:        "since-line skips first N lines",
			query:       url.Values{"since-line": []string{"3"}},
			wantContain: []string{"ERROR [sudph]", "free-form", "INFO [stcp]"},
			wantOmit:    []string{"DEBUG [tp:abc]: Serving", "INFO [sudph]: bound", "WARN [tp:abc]: stale"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			req := httptest.NewRequest("GET", "/visor.log?"+tc.query.Encode(), nil)
			c.Request = req

			streamFilteredVisorLog(c, logFile, tc.query)

			got := rec.Body.String()
			for _, want := range tc.wantContain {
				if !strings.Contains(got, want) {
					t.Errorf("response missing %q\nbody:\n%s", want, got)
				}
			}
			for _, omit := range tc.wantOmit {
				if strings.Contains(got, omit) {
					t.Errorf("response unexpectedly contains %q\nbody:\n%s", omit, got)
				}
			}
		})
	}
}
