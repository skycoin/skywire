// Package ui2 pkg/visor/ui2/i18n.go c3-vis-core
package ui2

import (
	"encoding/json"
	"io/fs"
	"strings"
	"sync"
)

// Lang is a language the pages offer.
type Lang struct {
	Code string
	Name string
}

// Langs follows the languages list of the Angular UI
// (static/skywire-manager-src/src/app/app.config.ts).
var Langs = []Lang{
	{"en", "English"},
	{"es", "Español"},
	{"de", "Deutsch"},
	{"pt", "Português (Brazil)"},
	{"zh", "中文"},
}

// LangCookie holds the chosen language. ui2.js keeps it in step with the
// Angular UI's "lang" localStorage entry.
const LangCookie = "ui2-lang"

// PickLang is the language named by the cookie, else the first supported one
// in an Accept-Language header, else English.
func PickLang(cookie, accept string) string {
	if known(cookie) {
		return cookie
	}
	for part := range strings.SplitSeq(accept, ",") {
		tag, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		tag, _, _ = strings.Cut(strings.ToLower(tag), "-")
		if known(tag) {
			return tag
		}
	}
	return "en"
}

func known(code string) bool {
	for _, l := range Langs {
		if l.Code == code {
			return true
		}
	}
	return false
}

// Catalog is the Angular UI's translation files, flattened to dotted keys.
type Catalog struct {
	fsys  fs.FS
	mu    sync.Mutex
	langs map[string]map[string]string
}

// NewCatalog reads assets/i18n/<lang>.json from fsys as each language is first
// asked for. A nil fsys gives the keys themselves.
func NewCatalog(fsys fs.FS) *Catalog {
	return &Catalog{fsys: fsys, langs: map[string]map[string]string{}}
}

// Get is key in lang, else in English, else the key itself.
func (c *Catalog) Get(lang, key string) string {
	if c != nil {
		if s := c.load(lang)[key]; s != "" {
			return s
		}
		if s := c.load("en")[key]; s != "" {
			return s
		}
	}
	return key
}

func (c *Catalog) load(lang string) map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m, ok := c.langs[lang]; ok {
		return m
	}
	m := map[string]string{}
	c.langs[lang] = m
	if c.fsys == nil || !known(lang) {
		return m
	}
	b, err := fs.ReadFile(c.fsys, "assets/i18n/"+lang+".json")
	if err != nil {
		return m
	}
	var tree map[string]any
	if json.Unmarshal(b, &tree) == nil {
		flatten("", tree, m)
	}
	return m
}

func flatten(prefix string, v any, out map[string]string) {
	switch t := v.(type) {
	case map[string]any:
		for k, sub := range t {
			if prefix != "" {
				k = prefix + "." + k
			}
			flatten(k, sub, out)
		}
	case string:
		out[prefix] = t
	}
}
