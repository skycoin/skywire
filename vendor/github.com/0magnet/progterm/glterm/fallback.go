package glterm

import (
	"os"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/image/font/opentype"
)

// fallbackFiles are system fonts tried for a character the terminal's own font
// lacks, after fontconfig: Android's, which also cover the half-width katakana
// a Matrix rain is made of.
var fallbackFiles = []string{
	"/system/fonts/NotoSansCJK-Regular.ttc",
	"/system/fonts/DroidSansFallbackFull.ttf",
	"/system/fonts/DroidSansFallback.ttf",
}

var (
	fallbackOnce sync.Once
	fallbackFont *opentype.Font
)

// fallback is the font for characters the terminal's own lacks, found the
// first time one is needed, or nil where there is none. It is read in place,
// as Android's is tens of megabytes.
func fallback() *opentype.Font {
	fallbackOnce.Do(func() {
		if out, ok := run("fc-match", "-f", "%{file}\n%{index}", "monospace:charset=ff71"); ok {
			file, index, _ := strings.Cut(out, "\n")
			n, _ := strconv.Atoi(index) //nolint:errcheck // 0 is the first face
			if f := openFont(file, n); f != nil {
				fallbackFont = f
				return
			}
		}
		for _, file := range fallbackFiles {
			if f := openFont(file, 0); f != nil {
				fallbackFont = f
				return
			}
		}
	})
	return fallbackFont
}

// openFont is face index of the font or collection in file, or nil. The file
// stays open for the font to read from.
func openFont(file string, index int) *opentype.Font {
	r, err := os.Open(file) //nolint:gosec // a system font
	if err != nil {
		return nil
	}
	if c, err := opentype.ParseCollectionReaderAt(r); err == nil {
		if f, err := c.Font(index); err == nil {
			return f
		}
	}
	r.Close() //nolint:errcheck,gosec // read-only
	return nil
}
