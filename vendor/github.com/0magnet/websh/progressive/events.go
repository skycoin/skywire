package progressive

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"sync"
	"time"
)

// What a program writes: placements over its cells, and messages to the
// widgets in them (PROTOCOL.md, Placements and Events). These only build the
// sequences; write them to the terminal between frames.

// Placement is what one placement shows over a rectangle of cells.
type Placement struct {
	Row int `json:"row"`
	Col int `json:"col"`
	W   int `json:"w"`
	H   int `json:"h"`
	// URL is an http(s) image; Widget a widget by name. One of the two.
	URL    string `json:"url,omitempty"`
	Widget string `json:"widget,omitempty"`
	// Fit is how an image fills its cells: "contain" (default), "cover",
	// "fill".
	Fit string `json:"fit,omitempty"`
	// Input gives the placement the mouse over it, instead of the program.
	Input bool `json:"input,omitempty"`
	// Page, with Input, lets the mouse over it go on to the page around the
	// terminal as well: for a page's own element moved into the cells, whose
	// handlers listen on the document.
	Page bool `json:"page,omitempty"`
	// Events asks the host to report what happens to the placement — clicks,
	// and messages from its widget — as input (Filter reads them).
	Events bool `json:"events,omitempty"`
}

// Place is the sequence laying p over its cells as id, or moving it there.
func Place(id string, p Placement) string { return osc("place;" + id + ";" + b64json(p)) }

// Remove is the sequence taking placement id away.
func Remove(id string) string { return osc("remove;" + id) }

// Clear is the sequence taking every placement away.
func Clear() string { return osc("clear") }

// Post is the sequence sending v, as JSON, to the widget in placement id. A
// widget that listens gets it on its message port.
func Post(id string, v any) string { return osc("post;" + id + ";" + b64json(v)) }

func osc(body string) string { return "\x1b]7337;" + body + "\x1b\\" }

func b64json(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte("null")
	}
	return base64.StdEncoding.EncodeToString(b)
}

// Event is something that happened to a placement made with Events, as the
// host reports it.
type Event struct {
	// ID is the placement's.
	ID string `json:"-"`
	// Type is "click", "dblclick", "contextmenu" (the host saw the person
	// do it) or "message" (the widget in it sent Data).
	Type string `json:"type"`
	// X and Y are where, as fractions of the placement (0 to 1); Col and Row
	// the cell under that point, counted on the screen.
	X   float64 `json:"x,omitempty"`
	Y   float64 `json:"y,omitempty"`
	Col int     `json:"col,omitempty"`
	Row int     `json:"row,omitempty"`
	// Button is the mouse button (0 is the main one).
	Button int `json:"button,omitempty"`
	// Data is what the widget sent, as JSON.
	Data json.RawMessage `json:"data,omitempty"`

	at time.Time
}

// When is when the event arrived. With it an Event is a tcell.Event, so a
// tcell program can post it into its own event queue and handle it in its
// one loop.
func (e *Event) When() time.Time { return e.at }

// EventSeq is the sequence a host writes into the program's input to report
// e on placement id.
func EventSeq(id string, e *Event) string { return osc("event;" + id + ";" + b64json(e)) }

const eventHead = "\x1b]7337;event;"

// Filter takes the host's events out of a terminal's input. Read the
// terminal through the returned reader — what was typed, and every other
// reply, pass through untouched — and receive the events on the channel,
// which is closed when r ends. The channel holds a few hundred events; past
// that, events are dropped rather than holding up the keys.
//
// An event split across reads is put back together. A lone Escape is never
// held back, so a program that times its Escape key still sees it at once.
func Filter(r io.Reader) (io.Reader, <-chan *Event) {
	f := &filter{r: r, ch: make(chan *Event, 256)}
	return f, f.ch
}

type filter struct {
	r      io.Reader
	ch     chan *Event
	held   []byte // the start of what may be an event, waiting for the rest
	out    []byte // filtered input not yet returned
	err    error
	closed sync.Once
}

func (f *filter) Read(p []byte) (int, error) {
	for len(f.out) == 0 {
		if f.err != nil {
			f.closed.Do(func() { close(f.ch) })
			if len(f.held) > 0 { // what was held was never an event
				f.out, f.held = f.held, nil
				break
			}
			return 0, f.err
		}
		buf := make([]byte, max(len(p), 512))
		n, err := f.r.Read(buf)
		f.err = err
		if n == 0 && err == nil {
			return 0, nil // a wake: no data, no error, as the source gave it
		}
		f.out = f.take(append(f.held, buf[:n]...))
	}
	n := copy(p, f.out)
	f.out = f.out[n:]
	return n, nil
}

// take removes the complete events from b, delivers them, and keeps back the
// start of one still arriving.
func (f *filter) take(b []byte) []byte {
	f.held = nil
	var out []byte
	for {
		i := bytes.Index(b, []byte(eventHead))
		if i < 0 {
			break
		}
		end, n := terminator(b[i+len(eventHead):])
		if end < 0 {
			out = append(out, b[:i]...)
			f.held = append([]byte{}, b[i:]...)
			return out
		}
		f.deliver(b[i+len(eventHead) : i+len(eventHead)+end])
		out = append(out, b[:i]...)
		b = b[i+len(eventHead)+end+n:]
	}
	// The tail may be the start of an event's head. Hold it only once it is
	// at least "ESC ] 7": a lone ESC, or ESC ] (Alt+]), is a key.
	for k := min(len(eventHead)-1, len(b)); k >= 3; k-- {
		if bytes.HasSuffix(b, []byte(eventHead[:k])) {
			f.held = append([]byte{}, b[len(b)-k:]...)
			return append(out, b[:len(b)-k]...)
		}
	}
	return append(out, b...)
}

func (f *filter) deliver(body []byte) {
	id, enc, ok := bytes.Cut(body, []byte(";"))
	if !ok {
		return
	}
	raw, err := base64.StdEncoding.DecodeString(string(enc))
	if err != nil {
		return
	}
	e := &Event{ID: string(id), at: time.Now()}
	if json.Unmarshal(raw, e) != nil {
		return
	}
	select {
	case f.ch <- e:
	default:
	}
}
