//go:build js && wasm

package web

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"syscall/js"
)

// A program's font (PROTOCOL.md, Font): it sends the file in chunks, the host
// loads it with the FontFace API and draws the terminal in it, and the
// program hears an ordinary resize as the cells are measured again. The host
// puts its own font back when the program exits. Remote output gets no say
// over the person's font.

// fontLimit is the most one font may be.
const fontLimit = 4 << 20

type fontState struct {
	part   []byte
	loaded map[string]string // content hash -> the family it was loaded as
	// The host's own font, while a program's is up.
	saved      bool
	family     string
	size       float64
	sizeChange bool
}

// font takes one font chunk, or "reset".
func (s *Session) font(arg string) {
	if s.remote() {
		return
	}
	if arg == "reset" {
		s.fontRestore()
		return
	}
	enc, chunk, _ := strings.Cut(arg, ";")
	var m struct {
		Family string  `json:"family"`
		Size   float64 `json:"size"`
		More   bool    `json:"more"`
	}
	mb, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || json.Unmarshal(mb, &m) != nil {
		return
	}
	b, err := base64.StdEncoding.DecodeString(chunk)
	if err != nil || len(s.fonts.part)+len(b) > fontLimit {
		s.fonts.part = nil
		return
	}
	s.fonts.part = append(s.fonts.part, b...)
	if m.More {
		return
	}
	data := s.fonts.part
	s.fonts.part = nil
	s.fontLoad(data, m.Size)
}

// fontLoad loads data, once per content, and switches the terminal to it.
// Its own name is made from its content, so it never stands in for a font
// the page has by the same family name.
func (s *Session) fontLoad(data []byte, size float64) {
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:8])
	if s.fonts.loaded == nil {
		s.fonts.loaded = map[string]string{}
	}
	if family, ok := s.fonts.loaded[hash]; ok {
		s.fontUse(family, size)
		return
	}
	family := "websh-font-" + hash
	buf := js.Global().Get("Uint8Array").New(len(data))
	js.CopyBytesToJS(buf, data)
	face := js.Global().Get("FontFace").New(family, buf)
	cmd := s.cmds
	var ok, fail js.Func
	release := func() { ok.Release(); fail.Release() }
	ok = js.FuncOf(func(js.Value, []js.Value) any {
		defer release()
		js.Global().Get("document").Get("fonts").Call("add", face)
		s.fonts.loaded[hash] = family
		if s.running && s.cmds == cmd { // still the program that sent it
			s.fontUse(family, size)
		}
		return nil
	})
	fail = js.FuncOf(func(_ js.Value, args []js.Value) any {
		defer release()
		js.Global().Get("console").Call("warn", "websh: a program's font did not load")
		return nil
	})
	face.Call("load").Call("then", ok, fail)
}

// fontUse draws the terminal in family, the host's own font behind it for
// any glyph it lacks.
func (s *Session) fontUse(family string, size float64) {
	f := &s.fonts
	if !f.saved {
		f.saved, f.family, f.size = true, s.Term.FontFamily(), s.Term.FontSize()
	}
	if size <= 0 {
		size = s.Term.FontSize()
	} else {
		f.sizeChange = true
	}
	s.Term.SetFont(strconv.Quote(family)+", "+f.family, size)
}

// fontRestore puts the host's own font back: as the program exits, or when
// it asks. The size goes back only if the program changed it, so a zoom the
// person made meanwhile stays.
func (s *Session) fontRestore() {
	f := &s.fonts
	f.part = nil
	if !f.saved {
		return
	}
	size := s.Term.FontSize()
	if f.sizeChange {
		size = f.size
	}
	s.Term.SetFont(f.family, size)
	f.saved, f.sizeChange = false, false
}
