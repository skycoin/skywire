package bitree

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// dispCol returns the display column (rune count) at which sub first appears in
// s, or -1 if absent.
func dispCol(s, sub string) int {
	i := strings.Index(s, sub)
	if i < 0 {
		return -1
	}
	return utf8.RuneCountInString(s[:i])
}

func ann(s string) []*Node { return []*Node{{Label: s}} }

func leaf(label string, cols ...string) *Node { return &Node{Label: label, Cols: cols} }

func check(t *testing.T, name, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s mismatch:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

// TestSingleRoute renders one route with a single flat left annotation.
func TestSingleRoute(t *testing.T) {
	root := &Node{Label: "this visor", Right: []*Node{{
		Label: "EXITPK", Cols: []string{"[stcpr]", "9a3a17e0…", "143ms"},
		Left: ann("R[0] ● 1.6K↑ 1.5K↓"),
	}}}
	want := "                     this visor" + "\n" +
		"                     │" + "\n" +
		"R[0] ● 1.6K↑ 1.5K↓ ──┴── EXITPK  [stcpr]  9a3a17e0…  143ms"
	check(t, "single", Render(root, Options{}), want)
}

// TestMockupTree reproduces the target route-group mockup: three routes on a
// vertical spine, each with a left metrics block and right topology that nests
// arbitrarily, with aligned trailing columns.
func TestMockupTree(t *testing.T) {
	root := &Node{Label: "this visor", Right: []*Node{
		{Label: "EXITPK", Cols: []string{"[stcpr]", "9a3a17e0…", "143ms"}, Left: ann("R[0] ● 1.6K↑ 1.5K↓")},
		{Label: "HOP1PK", Cols: []string{"[sudph]", "72512b4b…", "47ms"}, Left: ann("R[1] ● 0.5K↑ 0.4K↓"),
			Right: []*Node{leaf("EXITPK", "[stcpr]", "1fafe896…", "88ms")}},
		{Label: "HOP1PK", Cols: []string{"[webrtc]", "38252d68…", "61ms"}, Left: ann("R[2] ● 0.2K↑ 0.1K↓"),
			Right: []*Node{{Label: "HOP2PK", Cols: []string{"[squicr]", "85086f0c…", "74ms"},
				Right: []*Node{leaf("EXITPK", "[stcpr]", "da5c74a8…", "133ms")}}}},
	}}
	want := "                     this visor" + "\n" +
		"                     │" + "\n" +
		"R[0] ● 1.6K↑ 1.5K↓ ──┼── EXITPK          [stcpr]   9a3a17e0…  143ms" + "\n" +
		"R[1] ● 0.5K↑ 0.4K↓ ──┼── HOP1PK          [sudph]   72512b4b…  47ms" + "\n" +
		"                     │   └── EXITPK      [stcpr]   1fafe896…  88ms" + "\n" +
		"R[2] ● 0.2K↑ 0.1K↓ ──┴── HOP1PK          [webrtc]  38252d68…  61ms" + "\n" +
		"                         └── HOP2PK      [squicr]  85086f0c…  74ms" + "\n" +
		"                             └── EXITPK  [stcpr]   da5c74a8…  133ms"
	check(t, "mockup", Render(root, Options{}), want)
}

// TestLeftSubtreeNesting proves the left branch is a full nestable subtree:
// R[0] ● has two descendants, one of which nests a further level, all hanging
// at the same rows as the right branches.
func TestLeftSubtreeNesting(t *testing.T) {
	root := &Node{Label: "this visor", Right: []*Node{
		{Label: "EXITPK", Cols: []string{"[stcpr]", "9a3a17e0…", "143ms"}, Left: []*Node{{
			Label: "R[0] ●", Left: []*Node{
				{Label: "1.6K↑ 1.5K↓", Left: ann("rtt 143ms")},
				{Label: "via stcpr"},
			}}}},
		{Label: "HOP1PK", Cols: []string{"[sudph]", "72512b4b…", "47ms"}, Left: ann("R[1] ● 0.5K↑ 0.4K↓"),
			Right: []*Node{leaf("EXITPK", "[stcpr]", "1fafe896…", "88ms")}},
	}}
	want := "                     this visor" + "\n" +
		"                     │" + "\n" +
		"            R[0] ● ──┼── EXITPK      [stcpr]  9a3a17e0…  143ms" + "\n" +
		"   1.6K↑ 1.5K↓ ──┤   │" + "\n" +
		" rtt 143ms ──┘   │   │" + "\n" +
		"     via stcpr ──┘   │" + "\n" +
		"R[1] ● 0.5K↑ 0.4K↓ ──┴── HOP1PK      [sudph]  72512b4b…  47ms" + "\n" +
		"                         └── EXITPK  [stcpr]  1fafe896…  88ms"
	check(t, "nest", Render(root, Options{}), want)
}

// TestAlignColumns pads the tree portion to a fixed width so trailing columns
// line up even for a shallow tree.
func TestAlignColumns(t *testing.T) {
	root := &Node{Label: "root", Right: []*Node{
		{Label: "A", Cols: []string{"x"}, Left: ann("L")},
	}}
	got := Render(root, Options{AlignColumns: 20})
	// The single column "x" must appear at a fixed offset regardless of the
	// short "A" label: tree portion padded to 20 (+3 arm) then ColSep.
	want := "    root" + "\n" +
		"    │" + "\n" +
		"L ──┴── A                     x"
	check(t, "aligncols", got, want)
}

// TestHeaderRowAligns verifies the optional label-header row is drawn above the
// root and that its column labels line up EXACTLY with the data columns beneath
// (the "label tree" header — same peer-pk column, same trailing-column offsets).
func TestHeaderRowAligns(t *testing.T) {
	root := &Node{Label: "this visor", Right: []*Node{{
		Label: "EXITPK", Cols: []string{"[stcpr]", "9a3a17e0…", "143ms"},
		Left: ann("R[0] ● 1.6K↑ 1.5K↓"),
	}}}
	out := Render(root, Options{
		HeaderLeft:  "R[n]·state·rtt·bw",
		HeaderLabel: "peer-pk",
		HeaderCols:  []string{"[type]", "tp-id", "tp-rtt"},
	})
	lines := strings.Split(out, "\n")
	if len(lines) < 4 {
		t.Fatalf("expected header+root+descender+row, got %d lines:\n%s", len(lines), out)
	}
	hdr := lines[0]
	for _, lbl := range []string{"R[n]", "peer-pk", "[type]", "tp-id", "tp-rtt"} {
		if !strings.Contains(hdr, lbl) {
			t.Fatalf("header line missing %q: %q", lbl, hdr)
		}
	}
	if strings.Contains(hdr, "┼") || strings.Contains(hdr, "┬") || strings.Contains(hdr, "┴") {
		t.Errorf("header row must not carry a spine junction (reads as a route): %q", hdr)
	}
	var data string
	for _, l := range lines {
		if strings.Contains(l, "EXITPK") {
			data = l
		}
	}
	// peer label column == data PK label column; each trailing label column ==
	// its data column.
	if a, b := dispCol(hdr, "peer-pk"), dispCol(data, "EXITPK"); a != b {
		t.Errorf("peer-pk col %d != EXITPK col %d\nhdr:  %q\ndata: %q", a, b, hdr, data)
	}
	if a, b := dispCol(hdr, "[type]"), dispCol(data, "[stcpr]"); a != b {
		t.Errorf("[type] col %d != [stcpr] col %d\nhdr:  %q\ndata: %q", a, b, hdr, data)
	}
	if a, b := dispCol(hdr, "tp-rtt"), dispCol(data, "143ms"); a != b {
		t.Errorf("tp-rtt col %d != rtt col %d\nhdr:  %q\ndata: %q", a, b, hdr, data)
	}
}

// TestStyleCellPreservesLayout verifies that a zero-width styling wrapper does
// not shift columns: the layout is computed from plain text.
func TestStyleCellPreservesLayout(t *testing.T) {
	root := &Node{Label: "this visor", Right: []*Node{
		{Label: "EXITPK", Cols: []string{"[stcpr]"}, Left: ann("R[0]")},
	}}
	plain := Render(root, Options{})
	styled := Render(root, Options{StyleCell: func(s string, _ CellKind) string {
		return "\x1b[1m" + s + "\x1b[0m" // bold; zero display width
	}})
	// Strip ANSI from styled and compare to plain.
	stripped := ""
	inEsc := false
	for _, r := range styled {
		switch {
		case r == '\x1b':
			inEsc = true
		case inEsc && r == 'm':
			inEsc = false
		case !inEsc:
			stripped += string(r)
		}
	}
	check(t, "style", stripped, plain)
}
