// Package deskhost pkg/wasmhv/deskhost/desk_tour_text_test.go c4-wasm-desk
package deskhost

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDeskTourTextMatchesWiring keeps desk-tour.md and tourApps in step: every
// step in the file is wired, every wired id has a step, and each step has words.
func TestDeskTourTextMatchesWiring(t *testing.T) {
	steps, err := parseDeskTour(deskTourMD)
	require.NoError(t, err)
	var ids, wired []string
	for _, s := range steps {
		ids = append(ids, s.id)
		require.NotEmpty(t, s.title, s.id)
		require.NotEmpty(t, s.body, s.id)
	}
	for id := range tourApps {
		wired = append(wired, id)
	}
	sort.Strings(wired)
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	require.Equal(t, wired, sorted, "the ## ids in desk-tour.md and the keys of tourApps differ")
}

func TestDeskTourMarkdown(t *testing.T) {
	require.Equal(t, "a <code>&lt;pk&gt;.dmsg</code> <b>key</b> <i>is</i>", mdInline("a `<pk>.dmsg` **key** *is*"))
	require.Equal(t, "one two<br><br>three", mdBlock("one\ntwo\n\nthree\n"))
	require.Equal(t, "carriers:<ul><li><b>tcp</b> raw</li><li>ws</li></ul>after", mdBlock("carriers:\n\n- **tcp** raw\n- ws\n\nafter"))

	_, err := parseDeskTour("## a\nTitle: only a title\n")
	require.Error(t, err, "a step with no body")
}
