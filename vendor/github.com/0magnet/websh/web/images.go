//go:build js && wasm

package web

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"image"
	"image/color"
	_ "image/gif" // sizes and formats of pictures a program sends
	_ "image/jpeg"
	"image/png"
	"io"
	"math"
	"strconv"
	"strings"
	"syscall/js"

	xterm "github.com/0magnet/xterm-go"
)

// Inline images (PROTOCOL.md): pictures in the text, which scroll with it
// and stay in the scrollback — iTerm2's OSC 1337 File= with inline=1, and
// kitty's graphics protocol (APC G), the two that image tools speak. The
// terminal (xterm-go AddImage) anchors them; this reads the protocols.

// imageLimit is the largest picture taken, in bytes.
const imageLimit = 16 << 20

type imageState struct {
	// kitty: pictures transmitted by id, the one arriving in chunks, and
	// what each id is showing.
	stored  map[uint32]kittyImage
	part    []byte
	partCtl map[string]string
	shown   map[uint32][]*xterm.InlineImage
}

type kittyImage struct {
	url  string
	w, h int // pixels
}

// iterm2Inline shows an OSC 1337 File= picture with inline=1.
func (s *Session) iterm2Inline(args map[string]string, data []byte) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		cfg, format = image.Config{}, ""
	}
	url := blobURL(data, mimeOf(format))
	cols, rows := s.cellsFor(cfg.Width, cfg.Height, args["width"], args["height"], args["preserveAspectRatio"] != "0")
	s.Term.AddImage(url, cols, rows)
}

// cellsFor is how many cells a picture of w×h pixels covers, as asked: a
// width and height each "N" cells, "Npx", "N%" of the terminal, or "auto"
// (its own size, kept within the terminal's width).
func (s *Session) cellsFor(w, h int, ws, hs string, keepAspect bool) (cols, rows int) {
	cw, ch := s.Term.CellSize()
	tc, tr := s.Term.Core.Cols(), s.Term.Core.Rows()
	if cw <= 0 || ch <= 0 {
		return 0, 0
	}
	dpr := js.Global().Get("devicePixelRatio").Float()
	if dpr <= 0 {
		dpr = 1
	}
	// Its own size, in CSS pixels.
	pw, ph := float64(w)/dpr, float64(h)/dpr
	px := func(spec string, total, own, cell float64) (float64, bool) {
		switch {
		case spec == "" || spec == "auto":
			return own, false
		case strings.HasSuffix(spec, "px"):
			v, err := strconv.ParseFloat(strings.TrimSuffix(spec, "px"), 64)
			return v, err == nil
		case strings.HasSuffix(spec, "%"):
			v, err := strconv.ParseFloat(strings.TrimSuffix(spec, "%"), 64)
			return total * v / 100, err == nil
		default:
			v, err := strconv.ParseFloat(spec, 64)
			return v * cell, err == nil
		}
	}
	W, wSet := px(ws, float64(tc)*cw, pw, cw)
	H, hSet := px(hs, float64(tr)*ch, ph, ch)
	if keepAspect && pw > 0 && ph > 0 {
		switch {
		case wSet && !hSet:
			H = W * ph / pw
		case hSet && !wSet:
			W = H * pw / ph
		case wSet && hSet:
			s := math.Min(W/pw, H/ph)
			W, H = pw*s, ph*s
		}
	}
	if W <= 0 || H <= 0 { // a picture we could not read the size of
		W, H = float64(min(40, tc))*cw, float64(min(12, tr))*ch
	}
	if maxW := float64(tc) * cw; W > maxW { // within the terminal's width
		H, W = H*maxW/W, maxW
	}
	return max(1, int(math.Ceil(W/cw))), max(1, int(math.Ceil(H/ch)))
}

// kitty takes one APC G command: kitty's graphics protocol. Control keys
// (key=value, comma-separated) before ';', base64 data after. What is
// supported is what the tools that print pictures use: transmit (t), put
// (p), transmit and put (T), delete (d) and query (q); PNG (f=100) or raw
// RGB(A) (f=24/32, s and v its size), zlib-compressed (o=z); sent directly
// (t=d) in chunks (m=1). Files, temporary files and shared memory are not
// there to read in a tab, and are answered as kitty answers what it cannot
// do.
func (s *Session) kitty(data string) {
	if !strings.HasPrefix(data, "G") {
		return
	}
	ctlS, payload, _ := strings.Cut(data[1:], ";")
	ctl := map[string]string{}
	for _, kv := range strings.Split(ctlS, ",") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			ctl[k] = v
		}
	}
	st := &s.images
	// A continuation chunk carries only m (and q); the first chunk's keys
	// hold for the whole.
	if st.partCtl != nil {
		if q, ok := ctl["q"]; ok {
			st.partCtl["q"] = q
		}
		more := ctl["m"] == "1"
		ctl = st.partCtl
		ctl["m"] = map[bool]string{true: "1", false: "0"}[more]
	}
	b, err := base64.StdEncoding.DecodeString(payload)
	if err != nil && payload != "" {
		s.kittyReply(ctl, "EINVAL:bad base64")
		st.part, st.partCtl = nil, nil
		return
	}
	if ctl["m"] == "1" {
		if len(st.part)+len(b) > imageLimit {
			st.part, st.partCtl = nil, nil
			return
		}
		st.part = append(st.part, b...)
		st.partCtl = ctl
		return
	}
	if st.partCtl != nil {
		b = append(st.part, b...)
		st.part, st.partCtl = nil, nil
	}
	action := ctl["a"]
	if action == "" {
		action = "t"
	}
	id := parseU32(ctl["i"])
	switch action {
	case "q":
		if _, err := s.kittyDecode(ctl, b); err != "" {
			s.kittyReply(ctl, err)
		} else {
			s.kittyReply(ctl, "OK")
		}
	case "t", "T":
		img, errS := s.kittyDecode(ctl, b)
		if errS != "" {
			s.kittyReply(ctl, errS)
			return
		}
		if id != 0 {
			// An image sent again under its id replaces the old one, and
			// the old one's placements go with it, as in kitty.
			for _, h := range st.shown[id] {
				s.Term.RemoveImage(h)
			}
			delete(st.shown, id)
			if st.stored == nil {
				st.stored = map[uint32]kittyImage{}
			}
			st.stored[id] = img
		}
		if action == "T" {
			s.kittyPut(id, img, ctl)
		}
		s.kittyReply(ctl, "OK")
	case "p":
		img, ok := st.stored[id]
		if !ok {
			s.kittyReply(ctl, "ENOENT:no such image")
			return
		}
		s.kittyPut(id, img, ctl)
		s.kittyReply(ctl, "OK")
	case "d":
		switch ctl["d"] {
		case "", "a", "A":
			s.Term.ClearImages()
			st.shown = nil
			if ctl["d"] == "A" {
				st.stored = nil
			}
		case "i", "I":
			for _, h := range st.shown[id] {
				s.Term.RemoveImage(h)
			}
			delete(st.shown, id)
			if ctl["d"] == "I" {
				delete(st.stored, id)
			}
		}
	}
}

// kittyDecode reads a transmitted picture into something an <img> shows.
func (s *Session) kittyDecode(ctl map[string]string, b []byte) (kittyImage, string) {
	if t := ctl["t"]; t != "" && t != "d" {
		return kittyImage{}, "EBADF:only direct transmission in a browser"
	}
	if ctl["o"] == "z" {
		r, err := zlib.NewReader(bytes.NewReader(b))
		if err != nil {
			return kittyImage{}, "EINVAL:bad zlib data"
		}
		b, err = io.ReadAll(io.LimitReader(r, imageLimit))
		if err != nil {
			return kittyImage{}, "EINVAL:bad zlib data"
		}
	}
	switch f := ctl["f"]; f {
	case "", "32", "24":
		w, h := atoi(ctl["s"]), atoi(ctl["v"])
		n := 4
		if f == "24" {
			n = 3
		}
		if w <= 0 || h <= 0 || len(b) < w*h*n {
			return kittyImage{}, "EINVAL:raw data does not match its size"
		}
		img := image.NewNRGBA(image.Rect(0, 0, w, h))
		for i := 0; i < w*h; i++ {
			p := b[i*n:]
			a := uint8(255)
			if n == 4 {
				a = p[3]
			}
			img.SetNRGBA(i%w, i/w, color.NRGBA{p[0], p[1], p[2], a})
		}
		var out bytes.Buffer
		if png.Encode(&out, img) != nil {
			return kittyImage{}, "EINVAL:could not encode"
		}
		return kittyImage{url: blobURL(out.Bytes(), "image/png"), w: w, h: h}, ""
	case "100":
		cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
		if err != nil {
			return kittyImage{}, "EINVAL:not a PNG"
		}
		return kittyImage{url: blobURL(b, "image/png"), w: cfg.Width, h: cfg.Height}, ""
	}
	return kittyImage{}, "EINVAL:unknown format"
}

// kittyPut lays a picture at the cursor, over c×r cells if given.
func (s *Session) kittyPut(id uint32, img kittyImage, ctl map[string]string) {
	ws, hs := "auto", "auto"
	if c := atoi(ctl["c"]); c > 0 {
		ws = strconv.Itoa(c)
	}
	if r := atoi(ctl["r"]); r > 0 {
		hs = strconv.Itoa(r)
	}
	cols, rows := s.cellsFor(img.w, img.h, ws, hs, ws == "auto" || hs == "auto")
	b := s.Term.Core.Buffer()
	x, y := b.X, b.Y
	h := s.Term.AddImage(img.url, cols, rows)
	if ctl["C"] == "1" { // the cursor stays where it was
		b = s.Term.Core.Buffer()
		b.X, b.Y = x, y
	}
	if id != 0 && h != nil {
		if s.images.shown == nil {
			s.images.shown = map[uint32][]*xterm.InlineImage{}
		}
		s.images.shown[id] = append(s.images.shown[id], h)
	}
}

// kittyReply answers a command that named an id, unless told to be quiet
// (q=1 keeps errors only, q=2 nothing).
func (s *Session) kittyReply(ctl map[string]string, msg string) {
	id := ctl["i"]
	if id == "" || ctl["q"] == "2" || (ctl["q"] == "1" && msg == "OK") {
		return
	}
	s.Term.Core.Input("\x1b_Gi="+id+";"+msg+"\x1b\\", false)
}

// blobURL is an object URL for data, which the page keeps for as long as
// the picture may scroll back into view.
func blobURL(data []byte, mime string) string {
	buf := js.Global().Get("Uint8Array").New(len(data))
	js.CopyBytesToJS(buf, data)
	opts := map[string]any{}
	if mime != "" {
		opts["type"] = mime
	}
	blob := js.Global().Get("Blob").New(js.ValueOf([]any{buf}), opts)
	return js.Global().Get("URL").Call("createObjectURL", blob).String()
}

func mimeOf(format string) string {
	switch format {
	case "png", "jpeg", "gif":
		return "image/" + format
	}
	return ""
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func parseU32(s string) uint32 {
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0
	}
	return uint32(n)
}
