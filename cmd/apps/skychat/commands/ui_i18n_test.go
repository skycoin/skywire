// Package commands ui_i18n_test.go
package commands

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var i18nLangs = []string{"zh", "es", "fr"}

var (
	i18nPlaceholder = regexp.MustCompile(`\{\w+\}`)
	i18nWord        = regexp.MustCompile(`[A-Za-z]{2,}`)
)

// pageDictionaries decodes the page's translation block: lang -> English -> text.
func pageDictionaries(t *testing.T, page string) map[string]map[string]string {
	t.Helper()
	const open = `<script type="application/json" id="i18nData">`
	at := strings.Index(page, open)
	if at < 0 {
		t.Fatal("index.html has no i18nData block")
	}
	body := page[at+len(open):]
	end := strings.Index(body, "</script>")
	if end < 0 {
		t.Fatal("i18nData block is not closed")
	}
	var dicts map[string]map[string]string
	if err := json.Unmarshal([]byte(body[:end]), &dicts); err != nil {
		t.Fatalf("i18nData is not valid JSON: %v", err)
	}
	return dicts
}

func placeholders(s string) string {
	found := i18nPlaceholder.FindAllString(s, -1)
	sort.Strings(found)
	return strings.Join(found, ",")
}

// TestI18nDictionariesAgree pins that zh, es and fr translate the same English
// keys and keep each key's {placeholders}.
func TestI18nDictionariesAgree(t *testing.T) {
	dicts := pageDictionaries(t, embeddedPage(t))
	if len(dicts) != len(i18nLangs) {
		t.Fatalf("i18nData has %d languages, want %v", len(dicts), i18nLangs)
	}
	base := dicts[i18nLangs[0]]
	if len(base) == 0 {
		t.Fatal("the zh dictionary is empty")
	}
	for _, lang := range i18nLangs {
		dict, ok := dicts[lang]
		if !ok {
			t.Fatalf("i18nData has no %q dictionary", lang)
		}
		for key := range base {
			if _, ok := dict[key]; !ok {
				t.Errorf("%s is missing %q", lang, key)
			}
		}
		for key, text := range dict {
			if _, ok := base[key]; !ok {
				t.Errorf("%s has %q, which zh does not", lang, key)
			}
			if strings.TrimSpace(text) == "" {
				t.Errorf("%s translates %q as nothing", lang, key)
			}
			if placeholders(key) != placeholders(text) {
				t.Errorf("%s changes the placeholders of %q: %q", lang, key, text)
			}
			// Translations land in innerHTML and in quoted attributes.
			if strings.ContainsAny(text, "<>") || (strings.Contains(text, `"`) && !strings.Contains(key, `"`)) {
				t.Errorf("%s translation of %q carries markup or a bare quote: %q", lang, key, text)
			}
		}
	}
}

// jsSpan returns the index just past the JavaScript string literal that
// starts at i (a quote or backtick), or -1 when it does not close.
func jsSpan(src string, i int) int {
	q := src[i]
	for j := i + 1; j < len(src); j++ {
		switch src[j] {
		case '\\':
			j++
		case q:
			return j + 1
		case '\n':
			if q != '`' {
				return -1
			}
		}
	}
	return -1
}

// firstArg returns the source of the first argument of the call whose
// opening parenthesis is at open, skipping over strings and nested brackets.
func firstArg(src string, open int) string {
	depth := 0
	for j := open + 1; j < len(src); j++ {
		switch c := src[j]; c {
		case '\'', '"', '`':
			end := jsSpan(src, j)
			if end < 0 {
				return ""
			}
			j = end - 1
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth == 0 {
				return src[open+1 : j]
			}
			depth--
		case ',':
			if depth == 0 {
				return src[open+1 : j]
			}
		}
	}
	return ""
}

type jsLiteral struct {
	start, end int
	text       string
}

var jsComparison = regexp.MustCompile(`[!=]==?\s*$`)

// literals lists the string literals in src that are not the right-hand
// side of a comparison, joining 'a' + 'b' runs into one.
func literals(src string) []jsLiteral {
	var out []jsLiteral
	for i := 0; i < len(src); i++ {
		c := src[i]
		if c != '\'' && c != '"' && c != '`' {
			continue
		}
		end := jsSpan(src, i)
		if end < 0 {
			break
		}
		text := src[i+1 : end-1]
		if c != '`' {
			if s, err := strconv.Unquote(`"` + strings.ReplaceAll(strings.ReplaceAll(text, `\'`, `'`), `"`, `\"`) + `"`); err == nil {
				text = s
			}
		}
		if n := len(out); n > 0 && strings.TrimSpace(src[out[n-1].end:i]) == "+" {
			out[n-1].end, out[n-1].text = end, out[n-1].text+text
		} else if !jsComparison.MatchString(src[:i]) {
			out = append(out, jsLiteral{i, end, text})
		}
		i = end - 1
	}
	return out
}

var tCall = regexp.MustCompile(`(^|[^\w.$])t\(`)

// translatedSpans returns the source ranges of the first arguments of the
// t() calls in src.
func translatedSpans(src string) [][2]int {
	var spans [][2]int
	for _, m := range tCall.FindAllStringIndex(src, -1) {
		open := m[1] - 1
		arg := firstArg(src, open)
		spans = append(spans, [2]int{open + 1, open + 1 + len(arg)})
	}
	return spans
}

// i18nScript returns the script that follows the translation block.
func i18nScript(t *testing.T, page string) string {
	t.Helper()
	at := strings.Index(page, `id="i18nData"`)
	if at < 0 {
		t.Fatal("index.html has no i18nData block")
	}
	rest := page[at:]
	open := strings.Index(rest, "<script>")
	end := strings.Index(rest[open+1:], "</script>")
	if open < 0 || end < 0 {
		t.Fatal("index.html has no script after the i18nData block")
	}
	return rest[open : open+1+end]
}

func mainScript(t *testing.T, page string) string {
	t.Helper()
	at := strings.LastIndex(page, "<script>")
	end := strings.LastIndex(page, "</script>")
	if at < 0 || end < at {
		t.Fatal("index.html has no main script")
	}
	return page[at:end]
}

// TestI18nDialogsAreTranslated pins that toast and confirm/alert/prompt text goes
// through t(). confirm() is outside the DOM, so the page scan never reaches it.
func TestI18nDialogsAreTranslated(t *testing.T) {
	script := mainScript(t, embeddedPage(t))
	calls := regexp.MustCompile(`(^|[^\w$])(showToast|confirm|alert|prompt)\(`)
	checked := 0
	for _, m := range calls.FindAllStringSubmatchIndex(script, -1) {
		name := script[m[4]:m[5]]
		open := m[1] - 1
		// The method definition itself: showToast(message, type = 'success') {
		if strings.HasPrefix(script[open:], "(message, type") {
			continue
		}
		arg := firstArg(script, open)
		spans := translatedSpans(arg)
		line := strings.Count(script[:open], "\n") + 1
		for _, lit := range literals(arg) {
			if !i18nWord.MatchString(lit.text) {
				continue
			}
			inside := false
			for _, s := range spans {
				if lit.start >= s[0] && lit.end <= s[1] {
					inside = true
				}
			}
			if !inside {
				t.Errorf("main script line %d: %s(%s...) shows %q without t()", line, name, strings.TrimSpace(arg)[:min(40, len(strings.TrimSpace(arg)))], lit.text)
			}
		}
		checked++
	}
	if checked < 100 {
		t.Fatalf("found only %d toast/confirm calls; the scan is broken", checked)
	}
}

// TestI18nKeysAreTranslated pins that every English literal handed to t()
// has a dictionary entry, so a new string cannot ship untranslated.
func TestI18nKeysAreTranslated(t *testing.T) {
	page := embeddedPage(t)
	dict := pageDictionaries(t, page)["zh"]
	script := mainScript(t, page)
	seen := 0
	for _, m := range tCall.FindAllStringIndex(script, -1) {
		open := m[1] - 1
		for _, lit := range literals(firstArg(script, open)) {
			seen++
			if _, ok := dict[lit.text]; !ok {
				t.Errorf("t(%q) has no translation", lit.text)
			}
		}
	}
	if seen < 300 {
		t.Fatalf("found only %d t() keys; the scan is broken", seen)
	}
}

// TestI18nLongTextTranslates pins that the page scan has no length limit, so the
// long explanations in the settings dialogs translate like any label.
func TestI18nLongTextTranslates(t *testing.T) {
	page := embeddedPage(t)
	head := i18nScript(t, page)
	if regexp.MustCompile(`\.length\s*>\s*\d{3}`).MatchString(head) {
		t.Error("the i18n scan still skips text over a length limit")
	}
	if !strings.Contains(head, `.replace(/\s+/g, ' ').trim()`) {
		t.Error("the i18n scan no longer folds whitespace before the lookup")
	}
	long := 0
	for key := range pageDictionaries(t, page)["fr"] {
		if len(key) > 220 {
			long++
		}
	}
	if long < 5 {
		t.Errorf("only %d dictionary keys are longer than 220 characters", long)
	}
}

// TestI18nSkipsUserContent pins that the scan leaves people's words alone and
// follows later label changes without retranslating its own writes.
func TestI18nSkipsUserContent(t *testing.T) {
	page := embeddedPage(t)
	head := i18nScript(t, page)
	for _, want := range []string{
		"[data-no-i18n]",
		"characterData: true",
		"attributeFilter: ATTRS",
		"if (wroteText.get(node) === v) return;",
	} {
		if !strings.Contains(head, want) {
			t.Errorf("the i18n scan lost %q", want)
		}
	}
	if strings.Index(head, "if (!lang) return;") > strings.Index(head, "new MutationObserver") {
		t.Error("the i18n scan must not start without ?lang=")
	}
	for _, id := range []string{
		`id="messages"`, `id="recipients"`, `id="requests"`, `id="groups"`, `id="channels"`,
		`id="contacts"`, `id="searchResults"`, `id="toastContainer"`, `id="meName"`,
		`id="headerPk"`, `id="groupHeaderName"`, `id="fwdTargets"`, `id="savedPreview"`,
	} {
		tag := regexp.MustCompile(regexp.QuoteMeta(id) + `[^>]*>`).FindString(page)
		if !strings.Contains(tag, "data-no-i18n") {
			t.Errorf("%s holds user content but is not marked data-no-i18n", id)
		}
	}
}
