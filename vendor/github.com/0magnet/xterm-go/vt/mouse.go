package vt

import "fmt"

// Port of src/common/services/CoreMouseService.ts — mouse tracking
// reports with the protocols NONE/X10/VT200/DRAG/ANY and encodings
// DEFAULT/SGR/SGR_PIXELS. The DOM WheelEvent handling of the original
// lives in the browser layer.

// Mouse event types (CoreMouseEventType bit flags).
const (
	MouseEventNone  = 0
	MouseEventDown  = 1
	MouseEventUp    = 2
	MouseEventDrag  = 4
	MouseEventMove  = 8
	MouseEventWheel = 16
)

// Mouse buttons (CoreMouseButton).
const (
	MouseButtonLeft   = 0
	MouseButtonMiddle = 1
	MouseButtonRight  = 2
	MouseButtonNone   = 3
	MouseButtonWheel  = 4
	// additional buttons 8..11 are unsupported by the encodings
	MouseButtonAux1 = 8
)

// Mouse actions (CoreMouseAction).
const (
	MouseActionUp    = 0 // NOTE: this matches wheel up
	MouseActionDown  = 1 // NOTE: this matches wheel down
	MouseActionLeft  = 2
	MouseActionRight = 3
	MouseActionMove  = 32
)

// modifier bits in the event code
const (
	mouseModShift = 4
	mouseModAlt   = 8
	mouseModCtrl  = 16
)

// MouseEvent is the terminal-side mouse event (ICoreMouseEvent).
// Col/Row are 0-based grid coords; X/Y pixel coords (SGR_PIXELS).
type MouseEvent struct {
	Col, Row int
	X, Y     int
	Button   int
	Action   int
	Ctrl     bool
	Alt      bool
	Shift    bool
}

// mouseProtocol restricts which events get reported.
type mouseProtocol struct {
	events   int
	restrict func(e *MouseEvent) bool
}

// mouseEncoding renders an event to the report string ("" = no report).
type mouseEncoding func(e *MouseEvent) string

func mouseEventCode(e *MouseEvent, isSGR bool) int {
	code := 0
	if e.Ctrl {
		code |= mouseModCtrl
	}
	if e.Shift {
		code |= mouseModShift
	}
	if e.Alt {
		code |= mouseModAlt
	}
	if e.Button == MouseButtonWheel {
		code |= 64
		code |= e.Action
	} else {
		code |= e.Button & 3
		if e.Button&4 != 0 {
			code |= 64
		}
		if e.Button&8 != 0 {
			code |= 128
		}
		if e.Action == MouseActionMove {
			code |= MouseActionMove
		} else if e.Action == MouseActionUp && !isSGR {
			// special case - only SGR can report button on release
			code |= MouseButtonNone
		}
	}
	return code
}

var mouseProtocols = map[string]mouseProtocol{
	"NONE": {
		events:   MouseEventNone,
		restrict: func(e *MouseEvent) bool { return false },
	},
	"X10": {
		events: MouseEventDown,
		restrict: func(e *MouseEvent) bool {
			// no wheel, no move, no up
			if e.Button == MouseButtonWheel || e.Action != MouseActionDown {
				return false
			}
			// no modifiers
			e.Ctrl = false
			e.Alt = false
			e.Shift = false
			return true
		},
	},
	"VT200": {
		events: MouseEventDown | MouseEventUp | MouseEventWheel,
		restrict: func(e *MouseEvent) bool {
			return e.Action != MouseActionMove
		},
	},
	"DRAG": {
		events: MouseEventDown | MouseEventUp | MouseEventWheel | MouseEventDrag,
		restrict: func(e *MouseEvent) bool {
			// no move without button
			return e.Action != MouseActionMove || e.Button != MouseButtonNone
		},
	},
	"ANY": {
		events: MouseEventDown | MouseEventUp | MouseEventWheel |
			MouseEventDrag | MouseEventMove,
		restrict: func(e *MouseEvent) bool { return true },
	},
}

var mouseEncodings = map[string]mouseEncoding{
	// DEFAULT - CSI M Pb Px Py, single byte encoding, values up to 223.
	"DEFAULT": func(e *MouseEvent) string {
		p0 := mouseEventCode(e, false) + 32
		p1 := e.Col + 32
		p2 := e.Row + 32
		// suppress mouse report if we exceed addressable range
		if p0 > 255 || p1 > 255 || p2 > 255 {
			return ""
		}
		return "\x1b[M" + string(rune(p0)) + string(rune(p1)) + string(rune(p2))
	},
	// SGR - CSI < Pb ; Px ; Py M|m, no encoding limitation.
	"SGR": func(e *MouseEvent) string {
		final := "M"
		if e.Action == MouseActionUp && e.Button != MouseButtonWheel {
			final = "m"
		}
		return fmt.Sprintf("\x1b[<%d;%d;%d%s", mouseEventCode(e, true), e.Col, e.Row, final)
	},
	"SGR_PIXELS": func(e *MouseEvent) string {
		final := "M"
		if e.Action == MouseActionUp && e.Button != MouseButtonWheel {
			final = "m"
		}
		return fmt.Sprintf("\x1b[<%d;%d;%d%s", mouseEventCode(e, true), e.X, e.Y, final)
	},
}

// CoreMouseService decides whether/how to send mouse tracking reports.
type CoreMouseService struct {
	// OnProtocolChange fires with the event-type bitmask needed by the
	// new protocol (browser layer uses it to attach DOM listeners).
	OnProtocolChange func(events int)

	activeProtocol     string
	activeEncoding     string
	lastEvent          *MouseEvent
	wheelPartialScroll float64

	bufferService *BufferService
	coreService   *CoreService
	options       *Options
}

// NewCoreMouseService creates the mouse service with protocol NONE and
// encoding DEFAULT.
func NewCoreMouseService(bufferService *BufferService, coreService *CoreService, options *Options) *CoreMouseService {
	s := &CoreMouseService{
		bufferService: bufferService,
		coreService:   coreService,
		options:       options,
	}
	s.Reset()
	return s
}

// ActiveProtocol returns the current protocol name.
func (s *CoreMouseService) ActiveProtocol() string { return s.activeProtocol }

// ActiveEncoding returns the current encoding name.
func (s *CoreMouseService) ActiveEncoding() string { return s.activeEncoding }

// AreMouseEventsActive reports whether any events are being tracked.
func (s *CoreMouseService) AreMouseEventsActive() bool {
	return mouseProtocols[s.activeProtocol].events != 0
}

// SetActiveProtocol switches the protocol (panics on unknown name,
// like the JS throw).
func (s *CoreMouseService) SetActiveProtocol(name string) {
	p, ok := mouseProtocols[name]
	if !ok {
		panic("unknown protocol \"" + name + "\"")
	}
	s.activeProtocol = name
	if s.OnProtocolChange != nil {
		s.OnProtocolChange(p.events)
	}
}

// SetActiveEncoding switches the encoding.
func (s *CoreMouseService) SetActiveEncoding(name string) {
	if _, ok := mouseEncodings[name]; !ok {
		panic("unknown encoding \"" + name + "\"")
	}
	s.activeEncoding = name
}

// Reset restores protocol NONE / encoding DEFAULT.
func (s *CoreMouseService) Reset() {
	s.SetActiveProtocol("NONE")
	s.SetActiveEncoding("DEFAULT")
	s.lastEvent = nil
	s.wheelPartialScroll = 0
}

// ConsumeWheelEvent converts a wheel delta into a line scroll amount,
// accounting for partial trackpad scrolls (port of consumeWheelEvent;
// the DOM event fields are passed in by the browser layer).
// deltaMode: 0 = pixel, 1 = line, 2 = page.
func (s *CoreMouseService) ConsumeWheelEvent(deltaY float64, deltaMode int, shift, alt, ctrl bool, cellHeight, dpr float64) int {
	if deltaY == 0 || shift {
		return 0
	}
	if cellHeight == 0 || dpr == 0 {
		return 0
	}

	targetWheelEventPixels := cellHeight / dpr
	amount := deltaY * s.options.ScrollSensitivity
	if alt || ctrl {
		amount *= s.options.FastScrollSensitivity
	}

	switch deltaMode {
	case 0: // DOM_DELTA_PIXEL
		amount /= targetWheelEventPixels
		if deltaY < 50 && deltaY > -50 { // likely trackpad
			amount *= 0.3
		}
		s.wheelPartialScroll += amount
		whole := float64(int(abs(s.wheelPartialScroll)))
		if s.wheelPartialScroll < 0 {
			whole = -whole
		}
		s.wheelPartialScroll -= whole
		return int(whole)
	case 2: // DOM_DELTA_PAGE
		amount *= float64(s.bufferService.Rows)
	}
	return int(amount)
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// TriggerMouseEvent sends a mouse report if the active protocol and
// encoding allow it. Returns true if a report was sent (the browser
// layer uses this to decide whether to suppress the default action).
// Note: the method mutates the event to fulfill protocol restrictions.
func (s *CoreMouseService) TriggerMouseEvent(e *MouseEvent) bool {
	// range check for col/row
	if e.Col < 0 || e.Col >= s.bufferService.Cols ||
		e.Row < 0 || e.Row >= s.bufferService.Rows {
		return false
	}

	// filter nonsense combinations of button + action
	if e.Button == MouseButtonWheel && e.Action == MouseActionMove {
		return false
	}
	if e.Button == MouseButtonNone && e.Action != MouseActionMove {
		return false
	}
	if e.Button != MouseButtonWheel && (e.Action == MouseActionLeft || e.Action == MouseActionRight) {
		return false
	}

	// report 1-based coords
	e.Col++
	e.Row++

	// debounce move events at grid or pixel level
	if e.Action == MouseActionMove && s.lastEvent != nil &&
		s.equalEvents(s.lastEvent, e, s.activeEncoding == "SGR_PIXELS") {
		return false
	}

	// apply protocol restrictions
	if !mouseProtocols[s.activeProtocol].restrict(e) {
		return false
	}

	// encode report and send
	report := mouseEncodings[s.activeEncoding](e)
	if report != "" {
		// always send DEFAULT as binary data
		if s.activeEncoding == "DEFAULT" {
			s.coreService.TriggerBinaryEvent(report)
		} else {
			s.coreService.TriggerDataEvent(report, true)
		}
	}

	s.lastEvent = e
	return true
}

func (s *CoreMouseService) equalEvents(e1, e2 *MouseEvent, pixels bool) bool {
	if pixels {
		if e1.X != e2.X || e1.Y != e2.Y {
			return false
		}
	} else {
		if e1.Col != e2.Col || e1.Row != e2.Row {
			return false
		}
	}
	return e1.Button == e2.Button && e1.Action == e2.Action &&
		e1.Ctrl == e2.Ctrl && e1.Alt == e2.Alt && e1.Shift == e2.Shift
}
