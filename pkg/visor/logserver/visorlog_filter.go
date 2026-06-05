// Package logserver pkg/visor/logserver/visorlog_filter.go — server-side
// filtering for the /visor.log endpoint.
//
// /visor.log is logrus-formatted text on disk; over dmsg/skynet the wire is
// often the slowest hop, so filtering at the source — even at the cost of a
// regex per line — typically beats shipping the whole file to a client that's
// only going to grep it. The endpoint stays compatible when no query params
// are set (caller still receives the raw file via c.File).
package logserver

import (
	"bufio"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// logLevelRE captures `LEVEL` and `[module]` from a line shaped like
// `[2026-06-05T13:36:41Z] DEBUG [tp:abc]: Serving. …`. The level token
// is one of the logrus levels we accept; module is anything that's not
// a `]`. Lines that don't match are handled per the strict-filter rule
// in streamFilteredVisorLog.
var logLevelRE = regexp.MustCompile(`^\[[^\]]+\]\s+(TRACE|DEBUG|INFO|WARN|WARNING|ERROR|FATAL|PANIC)\s+\[([^\]]+)\]`)

// levelOrder is the canonical severity order used by --min-level. Higher
// = more severe. WARN and WARNING are aliased to the same rank so logs
// from libraries that emit either spell variant filter identically.
var levelOrder = map[string]int{
	"TRACE":   0,
	"DEBUG":   1,
	"INFO":    2,
	"WARN":    3,
	"WARNING": 3,
	"ERROR":   4,
	"FATAL":   5,
	"PANIC":   6,
}

// streamFilteredVisorLog walks logFile line-by-line, applies the filters
// declared in q, and writes matching lines to the gin response writer.
// Returns when the writer is closed, the limit is hit, the file ends
// (unless ?follow=1), or the context fires.
func streamFilteredVisorLog(c *gin.Context, logFile string, q map[string][]string) {
	getOne := func(k string) string {
		v := q[k]
		if len(v) == 0 {
			return ""
		}
		return v[0]
	}

	minLevel := strings.ToUpper(strings.TrimSpace(getOne("min-level")))
	minRank, hasMinLevel := levelOrder[minLevel]
	if minLevel != "" && !hasMinLevel {
		c.String(http.StatusBadRequest,
			"invalid min-level %q; expected one of trace, debug, info, warn, error, fatal, panic",
			minLevel)
		return
	}

	var modRE, grepRE *regexp.Regexp
	if s := getOne("module"); s != "" {
		re, err := regexp.Compile(s)
		if err != nil {
			c.String(http.StatusBadRequest, "invalid module regex: %v", err)
			return
		}
		modRE = re
	}
	if s := getOne("grep"); s != "" {
		re, err := regexp.Compile(s)
		if err != nil {
			c.String(http.StatusBadRequest, "invalid grep regex: %v", err)
			return
		}
		grepRE = re
	}

	sinceLine, _ := strconv.Atoi(getOne("since-line")) //nolint:errcheck
	if sinceLine < 0 {
		sinceLine = 0
	}
	limit, _ := strconv.Atoi(getOne("limit")) //nolint:errcheck
	follow := getOne("follow") == "1" || strings.EqualFold(getOne("follow"), "true")

	f, err := os.Open(logFile) //nolint:gosec // path comes from localPath config, not request
	if err != nil {
		c.String(http.StatusInternalServerError, "open visor.log: %v", err)
		return
	}
	defer f.Close() //nolint:errcheck

	c.Writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Flush()

	r := bufio.NewReaderSize(f, 64*1024)
	ctx := c.Request.Context()
	var lineNum, written int
	strictLevelMode := hasMinLevel // when set, lines that don't parse get dropped, not passed through

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line, err := r.ReadString('\n')
		if len(line) > 0 {
			lineNum++
			if lineNum > sinceLine && passesFilters(line, minRank, hasMinLevel, modRE, grepRE, strictLevelMode) {
				if _, werr := io.WriteString(c.Writer, line); werr != nil {
					return
				}
				c.Writer.Flush()
				written++
				if limit > 0 && written >= limit {
					return
				}
			}
		}
		if err == nil {
			continue
		}
		if err != io.EOF {
			return
		}
		// EOF — either we're done, or we follow and wait for more appends.
		if !follow {
			return
		}
		// Bounded poll. 250ms strikes a balance between latency and CPU.
		select {
		case <-ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// passesFilters returns true when line satisfies every active filter.
// strictLevelMode drops lines that don't match the standard log shape
// when a min-level filter is set (so a free-form library line can't
// sneak past --min-level=error).
func passesFilters(line string, minRank int, hasMinLevel bool, modRE, grepRE *regexp.Regexp, strictLevelMode bool) bool {
	if grepRE != nil && !grepRE.MatchString(line) {
		return false
	}
	if !hasMinLevel && modRE == nil {
		return true
	}
	m := logLevelRE.FindStringSubmatch(line)
	if m == nil {
		// Line didn't parse — drop on strict level filter, drop when caller
		// wanted a module match (no module = no match).
		if strictLevelMode || modRE != nil {
			return false
		}
		return true
	}
	if hasMinLevel {
		rank, ok := levelOrder[m[1]]
		if !ok || rank < minRank {
			return false
		}
	}
	if modRE != nil && !modRE.MatchString(m[2]) {
		return false
	}
	return true
}
