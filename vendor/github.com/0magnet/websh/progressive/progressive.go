// Package progressive is how a terminal program learns what its host can do
// beyond cells, by asking it — Discovery in websh's PROTOCOL.md. It is plain
// Go, for any terminal: in websh the answer says what the page offers (a
// placement, a widget, a font) and how far the host trusts this program's
// output; in any other terminal there is no answer, and the program draws
// its cells as ever.
//
// The probe sends the caps query and then DA1, which every terminal answers.
// Replies come back in order, so a caps reply before the DA1 reply means the
// host speaks the protocol, and a DA1 reply alone means it does not. Nothing
// is guessed and no timeout is needed except for a terminal that answers
// nothing at all.
package progressive

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

// Query asks the host for its capabilities.
const Query = "\x1b]7337;caps?\x1b\\"

// da1 is Primary Device Attributes, answered by every terminal.
const da1 = "\x1b[c"

// replyHead starts the host's answer.
const replyHead = "\x1b]7337;caps;"

// What a terminal that is not websh is asked, for what it can do beyond
// cells: kitty's graphics protocol (a one-pixel query, as kitty's
// documentation recommends; a terminal that has it answers OK), and its name
// (XTVERSION). Sixel support comes with the DA1 reply (attribute 4).
const (
	kittyQuery = "\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\"
	kittyReply = "\x1b_Gi=31;"
	xtversion  = "\x1b[>q"
	xtReply    = "\x1bP>|"
)

// Caps is what a host offers, as it says in its reply.
type Caps struct {
	V       int    `json:"v"`
	Host    string `json:"host"`
	Version string `json:"version,omitempty"`
	// Trust is the source the host gave this program's output: "page",
	// "local" or "remote" (PROTOCOL.md, Trust).
	Trust string `json:"trust"`
	// Cell is one cell's size in CSS pixels, and DPR the device pixel ratio.
	Cell struct {
		W float64 `json:"w"`
		H float64 `json:"h"`
	} `json:"cell"`
	DPR      float64  `json:"dpr"`
	Cols     int      `json:"cols"`
	Rows     int      `json:"rows"`
	Features []string `json:"features"`
	// Path is where a link that opened this program pointed in it (Page),
	// for the program to open there; empty for none.
	Path string `json:"path,omitempty"`
}

// LinkPath is where a link that opened this program pointed in it, or "".
func (c *Caps) LinkPath() string {
	if c == nil {
		return ""
	}
	return c.Path
}

// Has reports whether the host offers feature. A nil Caps offers nothing.
func (c *Caps) Has(feature string) bool {
	return c != nil && slices.Contains(c.Features, feature)
}

// Reply is c as a host answers the query with it.
func Reply(c *Caps) string {
	b, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	return replyHead + base64.StdEncoding.EncodeToString(b) + "\x1b\\"
}

var current atomic.Pointer[Caps]

// Current is what the last Probe found, or nil: no probe yet, or a host that
// offers nothing beyond cells.
func Current() *Caps { return current.Load() }

// Set records caps as found by other means: a program whose terminal is read
// by a library (tcell, say) finds the reply there and hands it over.
func Set(c *Caps) { current.Store(c) }

// ErrNoAnswer is a terminal that answered neither query in time.
var ErrNoAnswer = errors.New("progressive: the terminal did not answer")

// Probe asks the host over w, reads its answer from r and records it
// (Current). caps is nil where the host speaks no protocol.
//
// Read the terminal through in from then on, never r: it holds what was read
// besides the answers (keys typed meanwhile), and the read the probe still
// had waiting, so no key is lost and none is taken. Call Probe before
// anything else reads the terminal. A terminal that answers nothing is given
// up on after timeout, with err ErrNoAnswer and in still good to use.
func Probe(r io.Reader, w io.Writer, timeout time.Duration) (caps *Caps, in io.Reader, err error) {
	if _, err := io.WriteString(w, Query+kittyQuery+xtversion+da1); err != nil {
		return nil, r, err
	}
	a := &after{r: r, ch: make(chan chunk, 1)}
	go a.pump()
	deadline := time.After(timeout)
	for {
		select {
		case c, ok := <-a.ch:
			if !ok {
				return nil, a, io.ErrUnexpectedEOF
			}
			a.buf = append(a.buf, c.b...)
			if caps, rest, done := scan(a.buf); done {
				a.buf = rest
				a.stop.Store(true)
				current.Store(caps)
				return caps, a, nil
			}
			if c.err != nil {
				a.err = c.err
				a.stop.Store(true)
				return nil, a, c.err
			}
		case <-deadline:
			a.stop.Store(true)
			return nil, a, ErrNoAnswer
		}
	}
}

type chunk struct {
	b   []byte
	err error
}

// after is the terminal's input after a probe: what the probe read and did
// not use, then what its last read brings, then the terminal itself.
type after struct {
	r    io.Reader
	ch   chan chunk
	stop atomic.Bool
	buf  []byte
	err  error
	done bool // ch is drained and closed: read r directly
}

// pump reads for the probe, and stops after the read it is in when the
// probe is over.
func (a *after) pump() {
	defer close(a.ch)
	for !a.stop.Load() {
		b := make([]byte, 1024)
		n, err := a.r.Read(b)
		a.ch <- chunk{b[:n], err}
		if err != nil {
			return
		}
	}
}

func (a *after) Read(p []byte) (int, error) {
	for len(a.buf) == 0 && a.err == nil && !a.done {
		c, ok := <-a.ch
		if !ok {
			a.done = true
			break
		}
		a.buf, a.err = c.b, c.err
	}
	if len(a.buf) > 0 {
		n := copy(p, a.buf)
		a.buf = a.buf[n:]
		return n, nil
	}
	if a.err != nil {
		return 0, a.err
	}
	return a.r.Read(p)
}

// scan looks through what has been read for the replies. done is true
// once the DA1 reply — which every terminal sends, and sends last — is in;
// caps is what the replies said; rest is everything that was none of them.
func scan(buf []byte) (caps *Caps, rest []byte, done bool) {
	rest = buf
	var websh *Caps
	if body, r, ok, whole := cutString(rest, replyHead); ok {
		if !whole {
			return nil, buf, false
		}
		rest = r
		var c Caps
		if b, err := base64.StdEncoding.DecodeString(body); err == nil && json.Unmarshal(b, &c) == nil {
			websh = &c
		}
	}
	kitty := false
	if body, r, ok, whole := cutString(rest, kittyReply); ok {
		if !whole {
			return nil, buf, false
		}
		rest, kitty = r, body == "OK"
	}
	name := ""
	if body, r, ok, whole := cutString(rest, xtReply); ok {
		if !whole {
			return nil, buf, false
		}
		rest, name = r, body
	}
	attrs, r, found := cutDA1(rest)
	if !found {
		return nil, buf, false
	}
	rest = r
	caps = websh
	sixel := slices.Contains(attrs, "4")
	if caps == nil && (kitty || sixel || name != "") {
		caps = &Caps{Host: name, Trust: "local"} // a terminal, not a host: its own features only
	}
	if caps != nil {
		if kitty && !caps.Has("kitty-graphics") {
			caps.Features = append(caps.Features, "kitty-graphics")
		}
		if sixel && !caps.Has("sixel") {
			caps.Features = append(caps.Features, "sixel")
		}
	}
	return caps, rest, true
}

// cutString takes the string starting head (ended by ST or BEL) out of b.
// ok is whether head is there; whole whether its end has arrived too.
func cutString(b []byte, head string) (body string, rest []byte, ok, whole bool) {
	i := bytes.Index(b, []byte(head))
	if i < 0 {
		return "", b, false, false
	}
	tail := b[i+len(head):]
	end, n := terminator(tail)
	if end < 0 {
		return "", b, true, false
	}
	return string(tail[:end]), append(append([]byte{}, b[:i]...), tail[end+n:]...), true, true
}

// cutDA1 takes the DA1 reply (CSI ? attrs c) out of b, with its attributes.
func cutDA1(b []byte) (attrs []string, rest []byte, found bool) {
	for i := bytes.Index(b, []byte("\x1b[?")); i >= 0; {
		j := i + 3
		for j < len(b) && (b[j] >= '0' && b[j] <= '9' || b[j] == ';') {
			j++
		}
		if j == len(b) {
			return nil, b, false // part of a reply
		}
		if b[j] == 'c' {
			return strings.Split(string(b[i+3:j]), ";"), append(append([]byte{}, b[:i]...), b[j+1:]...), true
		}
		k := bytes.Index(b[j:], []byte("\x1b[?"))
		if k < 0 {
			break
		}
		i = j + k
	}
	return nil, b, false
}

// terminator finds the end of an OSC string: ST (ESC \) or BEL.
func terminator(b []byte) (at, n int) {
	for i, c := range b {
		switch {
		case c == 0x07:
			return i, 1
		case c == 0x1b && i+1 < len(b) && b[i+1] == '\\':
			return i, 2
		}
	}
	return -1, 0
}
