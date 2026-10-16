package glterm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/gofont/gomonobolditalic"
	"golang.org/x/image/font/gofont/gomonoitalic"
	"golang.org/x/image/font/opentype"
)

// DefaultPt is the size of a font named without one.
const DefaultPt = 10

// Font is a monospace typeface at a size in points, in its regular, bold,
// italic and bold italic styles.
type Font struct {
	Family string
	Pt     float64
	styles [4]*opentype.Font
}

// String is the font as ParseFont reads it, "DejaVu Sans Mono 10".
func (f Font) String() string {
	return f.Family + " " + strconv.FormatFloat(f.Pt, 'f', -1, 64)
}

// GoMono is the Go Mono font, which is built in.
func GoMono(pt float64) Font {
	f := Font{Family: "Go Mono", Pt: pt}
	for i, ttf := range [][]byte{gomono.TTF, gomonobold.TTF, gomonoitalic.TTF, gomonobolditalic.TTF} {
		parsed, err := opentype.Parse(ttf)
		if err != nil {
			panic(err) // the embedded fonts parse
		}
		f.styles[i] = parsed
	}
	return f
}

// ParseFont splits "Family Size", as a GTK font name is written, into the
// family and the size in points. A name with no size has DefaultPt.
func ParseFont(s string) (family string, pt float64) {
	s = strings.Trim(strings.TrimSpace(s), "'\"")
	if i := strings.LastIndexByte(s, ' '); i > 0 {
		if n, err := strconv.ParseFloat(s[i+1:], 64); err == nil && n > 0 {
			return strings.TrimSpace(s[:i]), n
		}
	}
	return s, DefaultPt
}

// LoadFont is the font spec names, "Family Size", or the desktop's terminal
// font when spec is empty (DesktopFont). On any failure it is Go Mono, at the
// size asked for, with the error saying why.
func LoadFont(spec string) (Font, error) {
	if spec == "" {
		spec = DesktopFont()
	}
	if spec == "" {
		return GoMono(DefaultPt), nil
	}
	family, pt := ParseFont(spec)
	if strings.EqualFold(family, "Go Mono") || family == "" {
		return GoMono(pt), nil
	}
	f, err := loadFamily(family, pt)
	if err != nil {
		return GoMono(pt), err
	}
	return f, nil
}

// DesktopFont is the font the desktop's terminal uses, where it can be found:
// xfce4-terminal's own, GNOME's monospace font, then fontconfig's monospace.
// It is empty where none is found.
func DesktopFont() string {
	if v, ok := run("xfconf-query", "-c", "xfce4-terminal", "-p", "/font-name"); ok && v != "" {
		if sys, _ := run("xfconf-query", "-c", "xfce4-terminal", "-p", "/font-use-system"); sys != "true" {
			return v
		}
		if v, ok := run("xfconf-query", "-c", "xsettings", "-p", "/Gtk/MonospaceFontName"); ok && v != "" {
			return v
		}
	}
	if v, ok := run("gsettings", "get", "org.gnome.desktop.interface", "monospace-font-name"); ok {
		if v = strings.Trim(v, "'"); v != "" {
			return v
		}
	}
	if v, ok := run("fc-match", "-f", "%{family[0]}", "monospace"); ok && v != "" {
		return v
	}
	return ""
}

// run is a command's trimmed output, and whether it succeeded.
func run(name string, args ...string) (string, bool) {
	if _, err := exec.LookPath(name); err != nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // fixed desktop tools, their arguments ours
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// loadFamily finds family's four styles with fontconfig. A style it does not
// have is drawn with the regular one.
func loadFamily(family string, pt float64) (Font, error) {
	f := Font{Family: family, Pt: pt}
	patterns := [4]string{"", ":weight=bold", ":slant=italic", ":weight=bold:slant=italic"}
	for i, p := range patterns {
		out, ok := run("fc-match", "-f", "%{file}\n%{index}\n%{family}\n%{weight}\n%{slant}", family+p)
		if !ok {
			if i == 0 {
				return f, errors.New("fc-match failed")
			}
			continue
		}
		parsed, err := parseMatch(out, family, i&1 != 0, i&2 != 0)
		if err != nil {
			if i == 0 {
				return f, err
			}
			continue
		}
		f.styles[i] = parsed
	}
	for i := 1; i < 4; i++ {
		if f.styles[i] == nil {
			f.styles[i] = f.styles[0]
			if i == 3 && f.styles[1] != f.styles[0] {
				f.styles[i] = f.styles[1]
			}
		}
	}
	return f, nil
}

// parseMatch reads the font file fc-match chose, if it is family in the
// style asked for: fontconfig always answers, with another font if it must.
func parseMatch(out, family string, bold, italic bool) (*opentype.Font, error) {
	fields := strings.Split(out, "\n")
	if len(fields) < 5 {
		return nil, fmt.Errorf("fc-match: %q", out)
	}
	file, families := fields[0], fields[2]
	weight, _ := strconv.Atoi(fields[3]) //nolint:errcheck // 0 reads as regular
	slant, _ := strconv.Atoi(fields[4])  //nolint:errcheck // as above
	found := false
	for _, name := range strings.Split(families, ",") {
		found = found || strings.EqualFold(strings.TrimSpace(name), family)
	}
	switch {
	case !found:
		return nil, fmt.Errorf("no font %q (fontconfig offers %q)", family, families)
	case bold && weight < 150: // FC_WEIGHT_DEMIBOLD is 180, MEDIUM 100
		return nil, errors.New("no bold")
	case italic && slant == 0:
		return nil, errors.New("no italic")
	}
	data, err := os.ReadFile(file) //nolint:gosec // the file fontconfig named
	if err != nil {
		return nil, err
	}
	index, _ := strconv.Atoi(fields[1]) //nolint:errcheck // 0 is the first face
	c, err := opentype.ParseCollection(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if index < 0 || index >= c.NumFonts() {
		index = 0
	}
	return c.Font(index)
}

// DpPerPt is how many of Gio's device independent pixels a point is: dp are
// 1/160 inch on Android, 1/72 on Apple's systems, and 1/96 elsewhere.
func DpPerPt() float64 {
	switch runtime.GOOS {
	case "android":
		return 160.0 / 72
	case "darwin", "ios":
		return 1
	}
	return 96.0 / 72
}
