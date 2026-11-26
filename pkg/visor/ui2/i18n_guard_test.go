package ui2

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode"

	"github.com/stretchr/testify/require"
)

// templLiteralWordsAllowed are words a page may write as is: product and
// protocol names and units. Anything else a user reads goes through T.
var templLiteralWordsAllowed = map[string]bool{
	"skywire": true, "skynet": true, "dmsg": true, "vpn": true, "localhost": true,
	"pk": true, "tcp": true, "udp": true, "stcp": true, "stcpr": true, "sudph": true,
	"http": true, "https": true, "socks5": true, "sky": true, "ms": true, "xpub": true,
}

// templLiteralTextAllowed are whole literals a page may write as is: shell
// commands, paths and example addresses. "{…}" stands for a Go expression.
var templLiteralTextAllowed = map[string]bool{
	"sudo skywire autoconfig": true,
	"{…} /health":             true,
	"skycoin:2jBbGx...":       true,
	"anyone@<pk>.skynet":      true,
}

// templTextAttrs are the attributes whose value a user reads.
var templTextAttrs = map[string]bool{
	"placeholder": true, "title": true, "aria-label": true, "alt": true,
	"hx-confirm": true, "hx-prompt": true, "label": true,
	"data-label": true, "data-confirm": true, "data-prompt": true,
	"data-done": true, "data-fmt": true, "data-empty": true,
}

var (
	templOpenRE    = regexp.MustCompile(`^templ \w+\(.*\) \{$`)
	templIconExpRE = regexp.MustCompile(`class=\{\s*"mi"`)
	templSkipRE    = regexp.MustCompile(`(?s)<script.*?</script>|<style.*?</style>|<!--.*?-->`)
	templAttrRE    = regexp.MustCompile(`([a-zA-Z_:][-a-zA-Z0-9_:.]*)="([^"]*)"`)
	templTypeRE    = regexp.MustCompile(`\btype="(submit|button|reset)"`)
	templIconRE    = regexp.MustCompile(`\bclass="mi\b`)
	templEntityRE  = regexp.MustCompile(`&[a-zA-Z]+;`)
)

// templSpokenAttrs are read by screen readers or shown as a dialog, so even a
// value without letters must come from T.
var templSpokenAttrs = map[string]bool{"title": true, "aria-label": true, "hx-confirm": true, "hx-prompt": true}

// templBodies returns the markup of every templ component in src, with Go
// expressions replaced by a NUL and control flow lines dropped.
func templBodies(src string) string {
	var out strings.Builder
	in := false
	for _, line := range strings.Split(src, "\n") {
		if !in {
			in = templOpenRE.MatchString(line)
			continue
		}
		if line == "}" {
			in = false
			out.WriteString("\n")
			continue
		}
		t := strings.TrimSpace(line)
		switch {
		case t == "}", t == "{", strings.HasPrefix(t, "{{"), strings.HasPrefix(t, "@"), strings.HasPrefix(t, "//"),
			strings.HasPrefix(t, "if "), strings.HasPrefix(t, "} else"), strings.HasPrefix(t, "for "),
			strings.HasPrefix(t, "switch "), strings.HasPrefix(t, "case "), strings.HasPrefix(t, "default:"):
			out.WriteString("\n")
			continue
		}
		out.WriteString(templStripExprs(templIconExpRE.ReplaceAllString(line, `class="mi" {`)))
		out.WriteString("\n")
	}
	return out.String()
}

// templStripExprs replaces each balanced { ... } and each inline
// @component(...) call with a NUL.
func templStripExprs(line string) string {
	var b strings.Builder
	open, closing := byte(0), byte(0)
	depth, quote := 0, byte(0)
	for i := 0; i < len(line); i++ {
		c := line[i]
		if depth == 0 {
			switch {
			case c == '{':
				open, closing, depth = '{', '}', 1
				b.WriteByte(0)
			case c == '@' && i+1 < len(line) && unicode.IsLetter(rune(line[i+1])):
				for i+1 < len(line) && line[i+1] != '(' && line[i+1] != '<' && line[i+1] != ' ' {
					i++
				}
				if i+1 < len(line) && line[i+1] == '(' {
					i++
					open, closing, depth = '(', ')', 1
				}
				b.WriteByte(0)
			default:
				b.WriteByte(c)
			}
			continue
		}
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '`' || c == '\'':
			quote = c
		case c == open:
			depth++
		case c == closing:
			depth--
		}
	}
	return b.String()
}

// templLiteral reports whether s, a text node or attribute value, holds
// words a user reads that are neither translated nor allowed.
func templLiteral(s string) bool {
	s = templEntityRE.ReplaceAllString(s, " ")
	if templLiteralTextAllowed[strings.Join(strings.Fields(strings.ReplaceAll(s, "\x00", "{…}")), " ")] {
		return false
	}
	for _, w := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) }) {
		if !templLiteralWordsAllowed[strings.ToLower(w)] {
			return true
		}
	}
	return false
}

func templShow(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\x00", "{…}")), " ")
}

// templUntranslated lists the user visible literals in one .templ source.
func templUntranslated(src string) []string {
	body := templSkipRE.ReplaceAllString(templBodies(src), "")
	var found []string
	icon := false
	for len(body) > 0 {
		lt := strings.IndexByte(body, '<')
		text := body
		if lt >= 0 {
			text = body[:lt]
		}
		if !icon && templLiteral(text) {
			found = append(found, "text: "+templShow(text))
		}
		if lt < 0 {
			break
		}
		end, quote := lt+1, byte(0)
		for ; end < len(body); end++ {
			c := body[end]
			if quote != 0 {
				if c == quote {
					quote = 0
				}
			} else if c == '"' {
				quote = c
			} else if c == '>' {
				break
			}
		}
		tag := body[lt:min(end+1, len(body))]
		body = body[min(end+1, len(body)):]
		name := strings.Fields(strings.TrimPrefix(tag, "<") + " ")[0]
		icon = templIconRE.MatchString(tag)
		for _, m := range templAttrRE.FindAllStringSubmatch(tag, -1) {
			attr, val := m[1], m[2]
			visible := templTextAttrs[attr] ||
				(attr == "value" && (name == "button" || (name == "input" && templTypeRE.MatchString(tag))))
			// A label read aloud must be words, so a bare symbol there needs T too.
			spoken := templSpokenAttrs[attr] && val != "" && !strings.Contains(val, "\x00")
			if visible && (spoken || templLiteral(val)) {
				found = append(found, attr+": "+templShow(val))
			}
		}
	}
	return found
}

// TestTemplTextTranslated fails when a page writes text a user reads
// without passing it through T, as the skynet tab did in zh.
func TestTemplTextTranslated(t *testing.T) {
	files, err := filepath.Glob("*.templ")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, f := range files {
		b, err := os.ReadFile(f) //nolint:gosec
		require.NoError(t, err)
		for _, s := range templUntranslated(string(b)) {
			t.Errorf("%s: untranslated %s", f, s)
		}
	}
}

func TestTemplUntranslatedFinds(t *testing.T) {
	src := "templ X() {\n\t<p>Hello { T(ctx, \"a.b\") }</p>\n\t<input placeholder=\"Remote port\"/>\n" +
		"\t<button type=\"submit\" value=\"Go\">{ T(ctx, \"a.c\") }</button>\n\t<option value=\"skynet\">skynet</option>\n" +
		"\tif x {\n\t\t<span>{ n } · 5</span>\n\t}\n\t<span class=\"mi\">add</span><button title=\"↩\">&rsaquo;</button><dd>@toggle(x, \"a b\")</dd>\n}\n"
	require.Equal(t, []string{"text: Hello {…}", "placeholder: Remote port", "value: Go", "title: ↩"}, templUntranslated(src))
}

var ui2KeyRE = regexp.MustCompile(`"([a-z0-9-]+(?:\.[a-z0-9-]+)+\.?)"`)

func i18nLeaves(t *testing.T, lang string) map[string]string {
	t.Helper()
	b, err := os.ReadFile("../static/assets/i18n/" + lang + ".json") //nolint:gosec
	require.NoError(t, err)
	var tree map[string]any
	require.NoError(t, json.Unmarshal(b, &tree))
	out := map[string]string{}
	flatten("", tree, out)
	return out
}

// TestUI2KeysTranslated fails when a translation key the pages use is
// missing from en.json or zh.json. Other languages fall back to English.
func TestUI2KeysTranslated(t *testing.T) {
	en := i18nLeaves(t, "en")
	tops := map[string]bool{}
	for k := range en {
		top, _, _ := strings.Cut(k, ".")
		tops[top] = true
	}
	files, err := filepath.Glob("*.templ")
	require.NoError(t, err)
	gos, err := filepath.Glob("*.go")
	require.NoError(t, err)
	handlers, err := filepath.Glob("../hypervisor_handlers_ui2*.go")
	require.NoError(t, err)
	keys := map[string]bool{}
	for _, f := range append(append(files, gos...), handlers...) {
		if strings.HasSuffix(f, "_templ.go") || strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f) //nolint:gosec
		require.NoError(t, err)
		for _, m := range ui2KeyRE.FindAllStringSubmatch(string(b), -1) {
			k := m[1]
			top, rest, _ := strings.Cut(k, ".")
			if tops[top] && !strings.Contains(".js.css.json.png.svg.html", "."+rest) {
				keys[k] = true
			}
		}
	}
	require.NotEmpty(t, keys)
	for _, lang := range []string{"en", "zh"} {
		have := i18nLeaves(t, lang)
		var missing []string
		for k := range keys {
			if !i18nKnown(have, k) {
				missing = append(missing, k)
			}
		}
		sort.Strings(missing)
		require.Empty(t, missing, "keys missing from %s.json", lang)
	}
}

// i18nKnown is whether key is a string in have, or the prefix of some when it
// ends in a dot or dash (a key the code completes, as vpn.templ does).
func i18nKnown(have map[string]string, key string) bool {
	if have[key] != "" {
		return true
	}
	if !strings.HasSuffix(key, ".") && !strings.HasSuffix(key, "-") {
		prefix := key + "."
		for k := range have {
			if strings.HasPrefix(k, prefix) {
				return true
			}
		}
		return false
	}
	for k := range have {
		if strings.HasPrefix(k, key) {
			return true
		}
	}
	return false
}
