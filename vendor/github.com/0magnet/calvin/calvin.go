// Package calvin calvin.go
/*
convert text to ascii art
*/
package calvin

import (
	"strings"
)

// $ awk '{if (NF == 0) next; if (/^.$/) {if (NR > 1) printf "},\n"; printf "\t\047%s\047: {", $0; getline; gsub(/\|$/, ""); printf "\042%s\042", $0; getline; gsub(/\|$/, ""); printf ", \042%s\042", $0; getline; gsub(/\|$/, ""); printf ", \042%s\042", $0}} END {print "}"}' calvin.txt

var boxFont = map[rune][]string{
	'a': {`┌─┐`, `├─┤`, `┴ ┴`},
	'b': {`┌┐ `, `├┴┐`, `└─┘`},
	'c': {`┌─┐`, `│  `, `└─┘`},
	'd': {`┌┬┐`, ` ││`, `─┴┘`},
	'e': {`┌─┐`, `├┤ `, `└─┘`},
	'f': {`┌─┐`, `├┤ `, `└  `},
	'g': {`┌─┐`, `│ ┬`, `└─┘`},
	'h': {`┬ ┬`, `├─┤`, `┴ ┴`},
	'i': {`┬`, `│`, `┴`},
	'j': {` ┬`, ` │`, `└┘`},
	'k': {`┬┌─`, `├┴┐`, `┴ ┴`},
	'l': {`┬  `, `│  `, `┴─┘`},
	'm': {`┌┬┐`, `│││`, `┴ ┴`},
	'n': {`┌┐┌`, `│││`, `┘└┘`},
	'o': {`┌─┐`, `│ │`, `└─┘`},
	'p': {`┌─┐`, `├─┘`, `┴  `},
	'q': {`┌─┐ `, `│─┼┐`, `└─┘└`},
	'r': {`┬─┐`, `├┬┘`, `┴└─`},
	's': {`┌─┐`, `└─┐`, `└─┘`},
	't': {`┌┬┐`, ` │ `, ` ┴ `},
	'u': {`┬ ┬`, `│ │`, `└─┘`},
	'v': {`┬  ┬`, `└┐┌┘`, ` └┘ `},
	'w': {`┬ ┬`, `│││`, `└┴┘`},
	'x': {`─┐ ┬`, `┌┴┬┘`, `┴ └─`},
	'y': {`┬ ┬`, `└┬┘`, ` ┴ `},
	'z': {`┌─┐`, `┌─┘`, `└─┘`},
	'A': {`╔═╗ `, `╠═╣ `, `╩ ╩ `},
	'B': {`╔╗  `, `╠╩╗ `, `╚═╝ `},
	'C': {`╔═╗ `, `║   `, `╚═╝ `},
	'D': {`╔╦╗ `, ` ║║ `, `═╩╝ `},
	'E': {`╔═╗ `, `║╣  `, `╚═╝ `},
	'F': {`╔═╗ `, `╠╣  `, `╚   `},
	'G': {`╔═╗ `, `║ ╦ `, `╚═╝ `},
	'H': {`╦ ╦ `, `╠═╣ `, `╩ ╩ `},
	'I': {`╦   `, `║   `, `╩   `},
	'J': {` ╦  `, ` ║  `, `╚╝  `},
	'K': {`╦╔═ `, `╠╩╗ `, `╩ ╩ `},
	'L': {`╦   `, `║   `, `╩═╝ `},
	'M': {`╔╦╗ `, `║║║ `, `╩ ╩ `},
	'N': {`╔╗╔ `, `║║║ `, `╝╚╝ `},
	'O': {`╔═╗ `, `║ ║ `, `╚═╝ `},
	'P': {`╔═╗ `, `╠═╝ `, `╩   `},
	'Q': {`╔═╗ `, `║═╬╗`, `╚═╝╚`},
	'R': {`╦═╗ `, `╠╦╝ `, `╩╚═ `},
	'S': {`╔═╗ `, `╚═╗ `, `╚═╝ `},
	'T': {`╔╦╗ `, ` ║  `, ` ╩  `},
	'U': {`╦ ╦ `, `║ ║ `, `╚═╝ `},
	'V': {`╦  ╦`, `╚╗╔╝`, ` ╚╝ `},
	'W': {`╦ ╦ `, `║║║ `, `╚╩╝ `},
	'X': {`═╗ ╦`, `╔╩╦╝`, `╩ ╚═`},
	'Y': {`╦ ╦ `, `╚╦╝ `, ` ╩  `},
	'Z': {`╔═╗ `, `╔═╝ `, `╚═╝ `},
	'!': {`┬    `, `│    `, `o    `},
	'@': {`┌─┐  `, `│└┘  `, `└──  `},
	'#': {`─┼─┼─`, `─┼─┼─`, `     `},
	'$': {`┌┼┐  `, `└┼┐  `, `└┼┘  `},
	'%': {`O┬   `, `┌┘   `, `┴O   `},
	'^': {`/\   `, `     `, `     `},
	'&': {` ┬   `, `┌┼─  `, `└┘   `},
	'*': {`\│/  `, `─ ─  `, `/│\  `},
	'-': {`   `, `───`, `   `},
	'_': {`    `, `    `, `────`},
	',': {` `, ` `, `┘`},
	'.': {` `, ` `, `o`},
	'?': {`┌─┐`, ` ┌┘`, ` o `},
	'[': {`┌─`, `│ `, `└─`},
	']': {`─┐`, ` │`, `─┘`},
	' ': {`  `, `  `, `  `},

	// Digits are an extension: Calvin S itself defines no glyphs for 0-9, so
	// patorjk.com/software/taag renders them as nothing. These follow the
	// font's lowercase style — light box-drawing, three rows, three columns —
	// and are shaped to stay distinct from the letters they most resemble:
	// 0 is barred so it does not read as o, 8 closes its lower bowl where a
	// has feet, and 2 keeps a flat base against z's closed one.
	'0': {`┌─┐`, `│││`, `└─┘`},
	'1': {` ┐ `, ` │ `, `─┴─`},
	'2': {`┌─┐`, `┌─┘`, `└──`},
	'3': {`┌─┐`, ` ─┤`, ` ─┘`},
	'4': {`┬ ┬`, `└─┤`, `  ┴`},
	'5': {`┌──`, `└─┐`, `└─┘`},
	'6': {`┌─ `, `├─┐`, `└─┘`},
	'7': {`──┐`, ` ┌┘`, ` ┴ `},
	'8': {`┌─┐`, `├─┤`, `└─┘`},
	'9': {`┌─┐`, `└─┤`, ` ─┘`},

	// Punctuation the reference font also lacks, in the same spirit as the
	// digits above. The slashes use ASCII, as ^ and * already do.
	'(':  {`┌`, `│`, `└`},
	')':  {`┐`, `│`, `┘`},
	':':  {` `, `o`, `o`},
	';':  {` `, `o`, `┘`},
	'\'': {`│`, ` `, ` `},
	'"':  {`││`, `  `, `  `},
	'/':  {`  /`, ` / `, `/  `},
	'\\': {`\  `, ` \ `, `  \`},

	// Braces are the brackets plus a notch: ┤ juts left, ├ juts right. Two
	// columns wide, so { lines up with the [ it has to sit beside.
	'{': {`┌─`, `┤ `, `└─`},
	'}': {`─┐`, ` ├`, `─┘`},
	'|': {`│`, `│`, `│`},

	// Both sit on the middle row, where - already lives. = takes the double
	// line the capitals use, which is what an equals sign is anyway.
	'+': {`   `, `─┼─`, `   `},
	'=': {`   `, `═══`, `   `},

	// Diagonals stepped into right angles, the way v and x already are. The
	// open third column mirrors how c and e are drawn.
	'<': {` ┌─`, `┌┘ `, `└──`},
	'>': {`─┐ `, ` └┐`, `──┘`},
	'~': {`    `, `┌─┐ `, `  └┘`},

	// ' is an upright tick, so ` leans, as / and \ do.
	'`': {`\ `, `  `, `  `},
}

// glyphRows is the height of every glyph in the font.
const glyphRows = 3

// tabWidth is how many spaces a tab expands to before rendering, since the
// font has no tab glyph.
const tabWidth = 4

// AsciiFont renders text in the Calvin S box-drawing font.
//
// Each line of the input becomes its own three-row block, so multi-line input
// renders as multi-line output. Line endings may be "\n", "\r\n" or "\r", and
// tabs expand to spaces. A single trailing newline is ignored, since piped
// input almost always ends with one and would otherwise render an empty block.
//
// Characters the font does not define are skipped. That matches the reference
// implementation at patorjk.com/software/taag — note in particular that
// Calvin S defines no digits, so "2026" renders as nothing at all.
func AsciiFont(input string) string {
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.ReplaceAll(input, "\r", "\n")
	input = strings.ReplaceAll(input, "\t", strings.Repeat(" ", tabWidth))
	input = strings.TrimSuffix(input, "\n")

	lines := strings.Split(input, "\n")
	output := make([]string, 0, len(lines)*glyphRows)

	for _, line := range lines {
		var rows [glyphRows]string
		for _, char := range line {
			glyph, ok := boxFont[char]
			if !ok {
				continue
			}
			for i := range rows {
				rows[i] += glyph[i]
			}
		}
		output = append(output, rows[:]...)
	}

	return strings.Join(output, "\n")
}

var charMap = map[rune]string{
	'A': "𝔸", 'B': "𝔹", 'C': "ℂ", 'D': "𝔻", 'E': "𝔼", 'F': "𝔽",
	'G': "𝔾", 'H': "ℍ", 'I': "𝕀", 'J': "𝕁", 'K': "𝕂", 'L': "𝕃",
	'M': "𝕄", 'N': "ℕ", 'O': "𝕆", 'P': "ℙ", 'Q': "ℚ", 'R': "ℝ",
	'S': "𝕊", 'T': "𝕋", 'U': "𝕌", 'V': "𝕍", 'W': "𝕎", 'X': "𝕏",
	'Y': "𝕐", 'Z': "ℤ",
	'a': "𝕒", 'b': "𝕓", 'c': "𝕔", 'd': "𝕕", 'e': "𝕖", 'f': "𝕗",
	'g': "𝕘", 'h': "𝕙", 'i': "𝕚", 'j': "𝕛", 'k': "𝕜", 'l': "𝕝",
	'm': "𝕞", 'n': "𝕟", 'o': "𝕠", 'p': "𝕡", 'q': "𝕢", 'r': "𝕣",
	's': "𝕤", 't': "𝕥", 'u': "𝕦", 'v': "𝕧", 'w': "𝕨", 'x': "𝕩",
	'y': "𝕪", 'z': "𝕫",
	'0': "𝟘", '1': "𝟙", '2': "𝟚", '3': "𝟛", '4': "𝟜",
	'5': "𝟝", '6': "𝟞", '7': "𝟟", '8': "𝟠", '9': "𝟡",
}

// BlackboardBold converts a string
func BlackboardBold(input string) string {
	var result strings.Builder
	for _, ch := range input {
		if specialChar, exists := charMap[ch]; exists {
			result.WriteString(specialChar)
		} else {
			result.WriteRune(ch)
		}
	}
	return result.String()
}
