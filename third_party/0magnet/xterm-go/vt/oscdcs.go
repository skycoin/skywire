package vt

// OSC parser states.
const (
	oscStart   = 0
	oscID      = 1
	oscPayload = 2
	oscAbort   = 3
)

// OscParser parses "OSC id ; payload ST/BEL" commands.
type OscParser struct {
	state     int
	active    []OscHandlerIface
	id        int
	handlers  map[int][]OscHandlerIface
	handlerFb func(id int, action string, data interface{})
}

// NewOscParser creates an OSC sub parser.
func NewOscParser() *OscParser {
	return &OscParser{
		id:        -1,
		handlers:  map[int][]OscHandlerIface{},
		handlerFb: func(id int, action string, data interface{}) {},
	}
}

// RegisterHandler adds a handler for the given OSC identifier.
func (o *OscParser) RegisterHandler(ident int, handler OscHandlerIface) {
	o.handlers[ident] = append(o.handlers[ident], handler)
}

// SetHandlerFallback installs the fallback handler.
func (o *OscParser) SetHandlerFallback(fn func(id int, action string, data interface{})) {
	o.handlerFb = fn
}

// Reset aborts any pending command.
func (o *OscParser) Reset() {
	if o.state == oscPayload {
		for j := len(o.active) - 1; j >= 0; j-- {
			o.active[j].End(false)
		}
	}
	o.active = nil
	o.id = -1
	o.state = oscStart
}

func (o *OscParser) startActive() {
	o.active = o.handlers[o.id]
	if len(o.active) == 0 {
		o.handlerFb(o.id, "START", nil)
	} else {
		for j := len(o.active) - 1; j >= 0; j-- {
			o.active[j].Start()
		}
	}
}

func (o *OscParser) putActive(data []uint32, start, end int) {
	if len(o.active) == 0 {
		o.handlerFb(o.id, "PUT", utf32ToString(data, start, end))
	} else {
		for j := len(o.active) - 1; j >= 0; j-- {
			o.active[j].Put(data, start, end)
		}
	}
}

// Start begins a new OSC command.
func (o *OscParser) Start() {
	o.Reset()
	o.state = oscID
}

// Put feeds data into the current OSC command.
func (o *OscParser) Put(data []uint32, start, end int) {
	if o.state == oscAbort {
		return
	}
	if o.state == oscID {
		for start < end {
			code := data[start]
			start++
			if code == 0x3b {
				o.state = oscPayload
				o.startActive()
				break
			}
			if code < 0x30 || code > 0x39 {
				o.state = oscAbort
				return
			}
			if o.id == -1 {
				o.id = 0
			}
			o.id = o.id*10 + int(code) - 48
		}
	}
	if o.state == oscPayload && end-start > 0 {
		o.putActive(data, start, end)
	}
}

// End finishes the OSC command; success indicates normal termination.
func (o *OscParser) End(success bool) {
	if o.state == oscStart {
		return
	}
	if o.state != oscAbort {
		// early end while still in ID state: announce START then END
		if o.state == oscID {
			o.startActive()
		}
		if len(o.active) == 0 {
			o.handlerFb(o.id, "END", success)
		} else {
			j := len(o.active) - 1
			for ; j >= 0; j-- {
				if o.active[j].End(success) {
					break
				}
			}
			j--
			// cleanup left over handlers
			for ; j >= 0; j-- {
				o.active[j].End(false)
			}
		}
	}
	o.active = nil
	o.id = -1
	o.state = oscStart
}

// OscHandler adapts a string-based callback into an OscHandlerIface.
type OscHandler struct {
	handler  func(data string) bool
	data     []rune
	hitLimit bool
}

// NewOscHandler wraps fn as an OSC handler.
func NewOscHandler(fn func(data string) bool) *OscHandler {
	return &OscHandler{handler: fn}
}

// Start implements OscHandlerIface.
func (h *OscHandler) Start() {
	h.data = h.data[:0]
	h.hitLimit = false
}

// Put implements OscHandlerIface.
func (h *OscHandler) Put(data []uint32, start, end int) {
	if h.hitLimit {
		return
	}
	for i := start; i < end; i++ {
		h.data = append(h.data, rune(data[i])) // #nosec G115 -- UTF-16 code units supplied by the parser
	}
	if len(h.data) > payloadLimit {
		h.data = h.data[:0]
		h.hitLimit = true
	}
}

// End implements OscHandlerIface.
func (h *OscHandler) End(success bool) bool {
	ret := false
	if !h.hitLimit && success {
		ret = h.handler(string(h.data))
	}
	h.data = h.data[:0]
	h.hitLimit = false
	return ret
}

// DcsParser parses DCS sequences (hook/put/unhook).
type DcsParser struct {
	handlers  map[int][]DcsHandler
	active    []DcsHandler
	ident     int
	handlerFb func(ident int, action string, data interface{})
}

// NewDcsParser creates a DCS sub parser.
func NewDcsParser() *DcsParser {
	return &DcsParser{
		handlers:  map[int][]DcsHandler{},
		handlerFb: func(ident int, action string, data interface{}) {},
	}
}

// RegisterHandler adds a handler for the given DCS identifier.
func (d *DcsParser) RegisterHandler(ident int, handler DcsHandler) {
	d.handlers[ident] = append(d.handlers[ident], handler)
}

// SetHandlerFallback installs the fallback handler.
func (d *DcsParser) SetHandlerFallback(fn func(ident int, action string, data interface{})) {
	d.handlerFb = fn
}

// Reset aborts any pending sequence.
func (d *DcsParser) Reset() {
	if len(d.active) > 0 {
		for j := len(d.active) - 1; j >= 0; j-- {
			d.active[j].Unhook(false)
		}
	}
	d.active = nil
	d.ident = 0
}

// Hook starts a DCS sequence.
func (d *DcsParser) Hook(ident int, params *Params) {
	d.Reset()
	d.ident = ident
	d.active = d.handlers[ident]
	if len(d.active) == 0 {
		d.handlerFb(d.ident, "HOOK", params)
	} else {
		for j := len(d.active) - 1; j >= 0; j-- {
			d.active[j].Hook(params)
		}
	}
}

// Put feeds payload data.
func (d *DcsParser) Put(data []uint32, start, end int) {
	if len(d.active) == 0 {
		d.handlerFb(d.ident, "PUT", utf32ToString(data, start, end))
	} else {
		for j := len(d.active) - 1; j >= 0; j-- {
			d.active[j].Put(data, start, end)
		}
	}
}

// Unhook finishes the DCS sequence.
func (d *DcsParser) Unhook(success bool) {
	if len(d.active) == 0 {
		d.handlerFb(d.ident, "UNHOOK", success)
	} else {
		j := len(d.active) - 1
		for ; j >= 0; j-- {
			if d.active[j].Unhook(success) {
				break
			}
		}
		j--
		for ; j >= 0; j-- {
			d.active[j].Unhook(false)
		}
	}
	d.active = nil
	d.ident = 0
}

// DcsHandlerFunc adapts a string-based callback into a DcsHandler.
type DcsHandlerFunc struct {
	handler  func(data string, params *Params) bool
	params   *Params
	data     []rune
	hitLimit bool
}

// NewDcsHandler wraps fn as a DCS handler.
func NewDcsHandler(fn func(data string, params *Params) bool) *DcsHandlerFunc {
	return &DcsHandlerFunc{handler: fn}
}

// Hook implements DcsHandler.
func (h *DcsHandlerFunc) Hook(params *Params) {
	h.params = params.Clone()
	h.data = h.data[:0]
	h.hitLimit = false
}

// Put implements DcsHandler.
func (h *DcsHandlerFunc) Put(data []uint32, start, end int) {
	if h.hitLimit {
		return
	}
	for i := start; i < end; i++ {
		h.data = append(h.data, rune(data[i])) // #nosec G115 -- UTF-16 code units supplied by the parser
	}
	if len(h.data) > payloadLimit {
		h.data = h.data[:0]
		h.hitLimit = true
	}
}

// Unhook implements DcsHandler.
func (h *DcsHandlerFunc) Unhook(success bool) bool {
	ret := false
	if !h.hitLimit && success {
		ret = h.handler(string(h.data), h.params)
	}
	h.params = nil
	h.data = h.data[:0]
	h.hitLimit = false
	return ret
}
