package vt

import "strconv"

// Sixel graphics: DCS P1 ; P2 ; P3 q <data> ST, the DEC bitmap format that
// libsixel, chafa, img2sixel, lsix, gnuplot and mpv draw pictures with.
//
// The decoding is here, with no browser in it, so it can be tested. What the
// terminal does with a decoded picture — laying it into the text — is the
// browser layer's, which takes it from Terminal.OnSixel; a terminal with no
// one listening there neither decodes sixel nor says in its DA1 reply that
// it can.
//
// The rules follow xterm.js's image addon where DEC left room for choice:
// raster attributes ("Pan;Pad;Ph;Pv) fix the picture's size, and anything
// drawn outside it is cut off; without them the picture grows to fit what is
// drawn. A sixel pixel is one pixel (the aspect ratio in P1 and in the raster
// attributes is not applied, as no encoder in use today asks for anything but
// 1:1). The color registers are shared by every picture and kept from one to
// the next until a full reset, and a register redefined after it was used
// changes only what is drawn after.

// Limits on one picture. Wider or taller than this and the rest is cut off;
// a longer sequence than this is abandoned. 4096 square is 64 MiB of pixels
// at most, and 25 MB of data is xterm.js's limit.
const (
	SixelMaxWidth  = 4096
	SixelMaxHeight = 4096
	sixelMaxData   = 25000000
	// SixelRegisters is how many color registers there are.
	SixelRegisters = 256
)

// SixelImage is a decoded sixel picture.
type SixelImage struct {
	Width, Height int
	// Pix is the picture as RGBA, four bytes a pixel, row by row. A pixel no
	// sixel set has alpha 0: transparent, unless Fill paints it.
	Pix []byte
	// Transparent is P2 = 1: what was not drawn stays see-through. Otherwise
	// it is the background — the one Attr carries, which is the background
	// text would be written on when the picture began.
	Transparent bool
	Attr        AttributeData
}

// Fill paints every pixel no sixel set in the color 0xRRGGBB.
func (img *SixelImage) Fill(rgb uint32) {
	r, g, b := byte(rgb>>16), byte(rgb>>8), byte(rgb) // #nosec G115 -- one channel each
	for i := 0; i+3 < len(img.Pix); i += 4 {
		if img.Pix[i+3] == 0 {
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = r, g, b, 255
		}
	}
}

// SixelPalette is the color registers, each 0xRRGGBB.
type SixelPalette [SixelRegisters]uint32

// DefaultSixelPalette is the VT340's sixteen colors, followed by the rest of
// xterm's 256-color palette for the registers the VT340 did not have.
func DefaultSixelPalette() SixelPalette {
	var p SixelPalette
	vt340 := [16][3]int{
		{0, 0, 0}, {20, 20, 80}, {80, 13, 13}, {20, 80, 20},
		{80, 20, 80}, {20, 80, 80}, {80, 80, 20}, {53, 53, 53},
		{26, 26, 26}, {33, 33, 60}, {60, 26, 26}, {33, 60, 33},
		{60, 33, 60}, {33, 60, 60}, {60, 60, 33}, {80, 80, 80},
	}
	for i, c := range vt340 {
		p[i] = pct(c[0], c[1], c[2])
	}
	steps := [6]uint32{0, 0x5f, 0x87, 0xaf, 0xd7, 0xff}
	for i := 16; i < 232; i++ {
		n := i - 16
		p[i] = steps[n/36]<<16 | steps[n/6%6]<<8 | steps[n%6]
	}
	for i := 232; i < 256; i++ {
		v := uint32(8 + (i-232)*10) // #nosec G115 -- 8..238
		p[i] = v<<16 | v<<8 | v
	}
	return p
}

// pct is a color given as percentages, as sixel gives them.
func pct(r, g, b int) uint32 {
	c := func(v int) uint32 {
		v = min(max(v, 0), 100)
		return uint32((v*255 + 50) / 100) // #nosec G115 -- 0..255
	}
	return c(r)<<16 | c(g)<<8 | c(b)
}

// hls is a color in sixel's HLS: hue in degrees with blue at 0, red at 120
// and green at 240 (DEC's, a third of a turn round from everyone else's);
// lightness and saturation in percent.
func hls(h, l, s int) uint32 {
	if s <= 0 {
		return pct(l, l, l)
	}
	hue := float64((h+240)%360) / 360
	lf := float64(min(max(l, 0), 100)) / 100
	sf := float64(min(s, 100)) / 100
	var q float64
	if lf < 0.5 {
		q = lf * (1 + sf)
	} else {
		q = lf + sf - lf*sf
	}
	p := 2*lf - q
	ch := func(t float64) uint32 {
		if t < 0 {
			t++
		}
		if t > 1 {
			t--
		}
		var v float64
		switch {
		case t < 1.0/6:
			v = p + (q-p)*6*t
		case t < 0.5:
			v = q
		case t < 2.0/3:
			v = p + (q-p)*(2.0/3-t)*6
		default:
			v = p
		}
		return uint32(v*255 + 0.5)
	}
	return ch(hue+1.0/3)<<16 | ch(hue)<<8 | ch(hue-1.0/3)
}

// SixelDecoder decodes one picture as its data arrives.
type SixelDecoder struct {
	palette *SixelPalette
	color   uint32 // the selected register's color

	x, y          int // where the next sixel goes; y is the top of the band
	width, height int // the extent of what was drawn
	rasterW       int // from the raster attributes; 0 if there were none
	rasterH       int
	drawn         bool // a sixel has been drawn, after which raster attributes are too late

	pix         []byte
	allocW      int // pix is allocW × allocH
	allocH      int
	transparent bool
	size        int
	aborted     bool
	cmd         byte // the command whose parameters are being read: # ! " or 0
	params      []int
	repeat      int
}

// NewSixelDecoder starts a picture drawn with the registers in palette,
// which it changes as the picture defines colors. transparent is P2 = 1.
func NewSixelDecoder(palette *SixelPalette, transparent bool) *SixelDecoder {
	d := &SixelDecoder{palette: palette, transparent: transparent, repeat: 1}
	d.color = palette[0]
	return d
}

// Write decodes more of the picture's data: everything between the q and
// the string terminator, in pieces of any size.
func (d *SixelDecoder) Write(data []uint32) {
	if d.aborted {
		return
	}
	d.size += len(data)
	if d.size > sixelMaxData {
		d.abort()
		return
	}
	for _, c := range data {
		if d.cmd != 0 {
			if c >= '0' && c <= '9' {
				n := &d.params[len(d.params)-1]
				if *n < 1<<20 {
					*n = *n*10 + int(c-'0')
				}
				continue
			}
			if c == ';' {
				d.params = append(d.params, 0)
				continue
			}
			d.finishCommand()
		}
		switch {
		case c >= 0x3f && c <= 0x7e:
			d.sixel(byte(c-0x3f), d.repeat) // #nosec G115 -- 0..63
			d.repeat = 1
		case c == '#' || c == '!' || c == '"':
			d.cmd = byte(c)
			d.params = append(d.params[:0], 0)
		case c == '$':
			d.x = 0
		case c == '-':
			d.x = 0
			d.y += 6
		}
	}
}

func (d *SixelDecoder) abort() {
	d.aborted = true
	d.pix = nil
}

func (d *SixelDecoder) finishCommand() {
	p := d.params
	switch d.cmd {
	case '!':
		d.repeat = max(p[0], 1)
	case '#':
		reg := p[0] % SixelRegisters
		if len(p) >= 5 {
			switch p[1] {
			case 1:
				d.palette[reg] = hls(p[2], p[3], p[4])
			case 2:
				d.palette[reg] = pct(p[2], p[3], p[4])
			}
		}
		d.color = d.palette[reg]
	case '"':
		// Pan;Pad;Ph;Pv. Only before the picture has begun: after that its
		// size is what was drawn.
		if !d.drawn && len(p) >= 4 && p[2] > 0 && p[3] > 0 {
			d.rasterW = min(p[2], SixelMaxWidth)
			d.rasterH = min(p[3], SixelMaxHeight)
			d.grow(d.rasterW, d.rasterH)
			d.width, d.height = d.rasterW, d.rasterH
		}
	}
	d.cmd = 0
}

// maxW and maxH are how far the picture may extend.
func (d *SixelDecoder) maxW() int {
	if d.rasterW > 0 {
		return d.rasterW
	}
	return SixelMaxWidth
}

func (d *SixelDecoder) maxH() int {
	if d.rasterH > 0 {
		return d.rasterH
	}
	return SixelMaxHeight
}

// grow makes room for a picture at least w × h, doubling so that a picture
// drawn without raster attributes is not copied once per sixel.
func (d *SixelDecoder) grow(w, h int) {
	if w <= d.allocW && h <= d.allocH {
		return
	}
	nw, nh := d.allocW, d.allocH
	if w > nw {
		nw = min(max(w, nw*2, 64), d.maxW())
	}
	if h > nh {
		nh = min(max(h, nh*2, 48), d.maxH())
	}
	pix := make([]byte, nw*nh*4)
	for row := range d.allocH {
		copy(pix[row*nw*4:], d.pix[row*d.allocW*4:(row+1)*d.allocW*4])
	}
	d.pix, d.allocW, d.allocH = pix, nw, nh
}

// sixel draws one sixel n times: six pixels stacked, bit 0 at the top.
func (d *SixelDecoder) sixel(bits byte, n int) {
	d.drawn = true
	x0 := d.x
	d.x += n
	if x0 >= d.maxW() || d.y >= d.maxH() {
		return
	}
	x1 := min(d.x, d.maxW())
	y1 := min(d.y+6, d.maxH())
	d.width = max(d.width, x1)
	d.height = max(d.height, y1)
	if bits == 0 {
		return
	}
	d.grow(x1, y1)
	r, g, b := byte(d.color>>16), byte(d.color>>8), byte(d.color) // #nosec G115 -- one channel each
	for i := range 6 {
		y := d.y + i
		if bits&(1<<i) == 0 || y >= y1 {
			continue
		}
		row := d.pix[y*d.allocW*4:]
		for x := x0; x < x1; x++ {
			o := x * 4
			row[o], row[o+1], row[o+2], row[o+3] = r, g, b, 255
		}
	}
}

// Image is the picture, or nil if there is none: nothing was drawn, or it
// was abandoned for being too big.
func (d *SixelDecoder) Image() *SixelImage {
	if d.cmd != 0 {
		d.finishCommand()
	}
	if d.aborted || d.width == 0 || d.height == 0 {
		return nil
	}
	d.grow(d.width, d.height)
	img := &SixelImage{Width: d.width, Height: d.height, Transparent: d.transparent}
	if d.allocW == d.width && d.allocH == d.height {
		img.Pix = d.pix
	} else {
		img.Pix = make([]byte, d.width*d.height*4)
		for row := range d.height {
			copy(img.Pix[row*d.width*4:(row+1)*d.width*4], d.pix[row*d.allocW*4:])
		}
	}
	d.pix = nil
	return img
}

// sixelHandler is the DCS q handler: it decodes as the data streams in,
// rather than gathering it into a string first, so a picture costs its
// pixels and not its encoding as well.
type sixelHandler struct {
	h   *InputHandler
	dec *SixelDecoder
}

func (s *sixelHandler) Hook(params *Params) {
	s.dec = nil
	if !s.h.sixelActive() {
		return
	}
	transparent := params.Length > 1 && params.Params[1] == 1
	s.dec = NewSixelDecoder(&s.h.sixelPalette, transparent)
}

func (s *sixelHandler) Put(data []uint32, start, end int) {
	if s.dec != nil {
		s.dec.Write(data[start:end])
	}
}

func (s *sixelHandler) Unhook(success bool) bool {
	dec := s.dec
	s.dec = nil
	if dec == nil || !success || !s.h.sixelActive() {
		return true
	}
	img := dec.Image()
	if img == nil {
		return true
	}
	img.Attr = *s.h.curAttrData
	s.h.OnSixel(img)
	return true
}

// sixelActive is whether there is sixel support to speak of: it is enabled,
// and someone is there to draw the pictures.
func (h *InputHandler) sixelActive() bool {
	return h.options.Sixel && h.OnSixel != nil && (h.sixelWanted == nil || h.sixelWanted())
}

// Graphics attribute items and statuses of XTSMGRAPHICS.
const (
	gaColors   = 1
	gaSixelGeo = 2
	gaSuccess  = 0
	gaErrItem  = 1
	gaErrAct   = 2
	gaFailure  = 3
)

// GraphicsAttributes handles XTSMGRAPHICS (CSI ? Pi ; Pa ; Pv S), with which
// a program asks how many color registers there are and how big a picture
// may be. The register count is fixed, so only reading it, resetting it and
// setting it to what it already is succeed.
func (h *InputHandler) GraphicsAttributes(params *Params) bool {
	if !h.sixelActive() {
		return false
	}
	if params.Length < 2 {
		return true
	}
	item, action := int(params.Params[0]), int(params.Params[1])
	reply := func(s ...int) {
		out := c0ESC + "[?" + strconv.Itoa(item)
		for _, v := range s {
			out += ";" + strconv.Itoa(v)
		}
		h.coreService.TriggerDataEvent(out+"S", false)
	}
	switch item {
	case gaColors:
		switch action {
		case 1, 2, 4: // read, reset to default, read maximum
			reply(gaSuccess, SixelRegisters)
		case 3: // set
			if params.Length > 2 && int(params.Params[2]) == SixelRegisters {
				reply(gaSuccess, SixelRegisters)
			} else {
				reply(gaFailure)
			}
		default:
			reply(gaErrAct)
		}
	case gaSixelGeo:
		switch action {
		case 1: // read: the text area, if anyone knows it, within the limits
			w, hgt := SixelMaxWidth, SixelMaxHeight
			if h.OnSixelGeometry != nil {
				if cw, ch := h.OnSixelGeometry(); cw > 0 && ch > 0 {
					w, hgt = min(cw, w), min(ch, hgt)
				}
			}
			reply(gaSuccess, w, hgt)
		case 4: // read maximum
			reply(gaSuccess, SixelMaxWidth, SixelMaxHeight)
		default:
			reply(gaErrAct)
		}
	default:
		reply(gaErrItem)
	}
	return true
}
