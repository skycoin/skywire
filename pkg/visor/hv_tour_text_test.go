// Package visor pkg/visor/hv_tour_text_test.go c3-vis-core
package visor

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestHVTourTextMatchesWiring keeps the dashboard tour's words (hv-tour.md) and
// its wiring (hv-tour.js) in step, on the built copy this package embeds.
func TestHVTourTextMatchesWiring(t *testing.T) {
	md, err := os.ReadFile("static/assets/tour/hv-tour.md")
	require.NoError(t, err)
	js, err := os.ReadFile("static/assets/tour/hv-tour.js")
	require.NoError(t, err)

	var ids []string
	for _, m := range regexp.MustCompile(`(?m)^## (\S+)\s*$`).FindAllStringSubmatch(string(md), -1) {
		ids = append(ids, m[1])
	}
	src := string(js)
	start, end := strings.Index(src, "function wiring("), strings.Index(src, "function mdInline(")
	require.True(t, start >= 0 && end > start, "hv-tour.js has no wiring table")
	var wired []string
	for _, m := range regexp.MustCompile(`(?m)^\s*"([a-z0-9-]+)":\s*\{`).FindAllStringSubmatch(src[start:end], -1) {
		wired = append(wired, m[1])
	}
	require.NotEmpty(t, ids)
	sort.Strings(ids)
	sort.Strings(wired)
	require.Equal(t, wired, ids, "the ## ids in hv-tour.md and the wiring in hv-tour.js differ")
}
