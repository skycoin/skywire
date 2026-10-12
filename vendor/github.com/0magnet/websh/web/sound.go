//go:build js && wasm

package web

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"syscall/js"
)

// Sound (PROTOCOL.md): a program plays a sound by URL or by bytes it sends,
// by an id it can stop it by, at a volume, once or looping. Every sound a
// command started stops when it ends.

// soundLimit is the most a command may send as sound bytes.
const soundLimit = 8 << 20

type soundState struct {
	playing map[string]sound
	part    []byte
	size    int
}

type sound struct {
	el  js.Value
	url string // an object URL to let go, or ""
}

// sound takes `OSC 7337 ; sound ; <meta> [; <chunk>]`.
func (s *Session) sound(arg string) {
	enc, chunk, _ := strings.Cut(arg, ";")
	var m struct {
		ID     string   `json:"id"`
		URL    string   `json:"url"`
		Type   string   `json:"type"`
		Volume *float64 `json:"volume"`
		Loop   bool     `json:"loop"`
		Stop   bool     `json:"stop"`
		More   bool     `json:"more"`
	}
	mb, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || json.Unmarshal(mb, &m) != nil {
		return
	}
	st := &s.sounds
	if m.Stop {
		s.soundStop(m.ID)
		return
	}
	src, obj := "", ""
	switch {
	case m.URL != "":
		if !strings.HasPrefix(m.URL, "https://") && !strings.HasPrefix(m.URL, "http://") {
			return
		}
		src = m.URL
	default:
		b, err := base64.StdEncoding.DecodeString(chunk)
		if err != nil || st.size+len(b) > soundLimit {
			st.part = nil
			return
		}
		st.size += len(b)
		st.part = append(st.part, b...)
		if m.More {
			return
		}
		buf := js.Global().Get("Uint8Array").New(len(st.part))
		js.CopyBytesToJS(buf, st.part)
		st.part = nil
		typ := m.Type
		if typ == "" {
			typ = "audio/mpeg"
		}
		blob := js.Global().Get("Blob").New(js.ValueOf([]any{buf}), map[string]any{"type": typ})
		obj = js.Global().Get("URL").Call("createObjectURL", blob).String()
		src = obj
	}
	s.soundStop(m.ID) // the same id again replaces it
	a := js.Global().Get("Audio").New(src)
	if m.Volume != nil {
		a.Set("volume", max(0, min(1, *m.Volume)))
	}
	a.Set("loop", m.Loop)
	p := a.Call("play")
	once(func() {}, func(f js.Value) { p.Call("catch", f) }) // a page the person has not touched may refuse
	if st.playing == nil {
		st.playing = map[string]sound{}
	}
	st.playing[m.ID] = sound{el: a, url: obj}
}

func (s *Session) soundStop(id string) {
	if p, ok := s.sounds.playing[id]; ok {
		p.el.Call("pause")
		if p.url != "" {
			js.Global().Get("URL").Call("revokeObjectURL", p.url)
		}
		delete(s.sounds.playing, id)
	}
}

// soundsEnd stops every sound, as the command ends.
func (s *Session) soundsEnd() {
	for id := range s.sounds.playing {
		s.soundStop(id)
	}
	s.sounds = soundState{}
}
