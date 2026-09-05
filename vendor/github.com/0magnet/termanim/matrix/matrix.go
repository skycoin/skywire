// Package matrix is falling columns of glyphs.
//
// Written from the effect rather than from any implementation of it. Columns of
// characters fall at differing speeds and the trail fades behind the leading
// glyph — which is the description everyone starts from, and it is where most
// reproductions stop. Carl Newton's frame-by-frame analysis of the 1999 title
// sequence turns up several things that description misses, and they are what
// separates this from a green screensaver:
//
//   - ONLY ABOUT ONE STREAM IN FIVE leads with a highlighted glyph, and only
//     its leading glyph is ever highlighted. Whitening every head — the obvious
//     reading — makes a row of white dots racing down the screen. In the film
//     most of the rain is plain green and the white heads are scattered.
//
//   - THE ALPHABET TURNS OVER ON A SHARED BEAT. A glyph holds for three frames
//     and then changes, and every glyph that is going to change changes on the
//     same frame. Flickering each one to its own clock reads as static; the
//     original ticks over all at once and is still in between.
//
//   - THE HIGHLIGHTED GLYPHS STAMMER. Every so often all of them hesitate at
//     the same moment, so the streams carrying them drop a row behind the ones
//     that did not. It is the least obvious behavior in the original and the
//     reason the white heads do not stay in formation.
//
//   - The alphabet is half-width katakana and numerals plus exactly one letter
//     of the English alphabet, Z.
//
// One thing cannot be fixed here. The film's katakana are MIRRORED, drawn as a
// custom typeface by Simon Whiteley, and Unicode encodes no mirrored katakana —
// so a terminal has no way to ask for them. It is the detail reproductions are
// most often pulled up on and the only one out of reach without shipping a font.
//
//	https://carlnewton.github.io/digital-rain-analysis/
//
// Deliberately not derived from TMatrix, neo or cmatrix, all of which are
// copyleft. Nothing here needs to be.
package matrix

import (
	"math/rand"

	"github.com/gdamore/tcell/v3"

	"github.com/0magnet/termanim/canvas"
)

// Glyphs is the default alphabet: half-width katakana, digits, and the letter
// Z. Half-width so every glyph is one cell — full-width kana would occupy two
// and the columns would not line up.
//
// The film's alphabet is a custom typeface by Simon Whiteley, and the katakana
// in it are MIRRORED — flipped horizontally. That is the detail reproductions
// are most often pulled up on, and it is the one thing here that cannot be
// fixed: Unicode encodes no mirrored katakana, so a terminal has no way to ask
// for them. Drawing them would mean shipping a font, which an animation that
// runs in whatever terminal you already have cannot do.
//
// Z is here because it is the only letter of the English alphabet in the film's
// set. The numerals are in it too, some of them flipped, which again is not
// something that can be asked for.
var Glyphs = []rune(
	"ｱｲｳｴｵｶｷｸｹｺｻｼｽｾｿﾀﾁﾂﾃﾄﾅﾆﾇﾈﾉﾊﾋﾌﾍﾎﾏﾐﾑﾒﾓﾔﾕﾖﾗﾘﾙﾚﾛﾜﾝ0123456789Z")

// column is one falling stream.
type column struct {
	head   float64 // row of the leading glyph, fractional so speeds can differ
	speed  float64 // rows per simulation step
	length int     // trail length in rows
	active bool

	// hot marks a stream whose leading glyph is highlighted — drawn white and
	// bold rather than in the palette's brightest green.
	//
	// Only about one stream in five has one, and only its LEADING glyph is ever
	// highlighted. Highlighting every stream, which is the easy thing to do and
	// what this did, turns the screen into a row of white dots chasing each
	// other down it; in the film most of the rain is plain green and the white
	// heads are scattered through it.
	hot bool

	// word is the stream's word when Words is set, and next is how far
	// through it the head has got. Empty means this stream is glyph rain.
	word []rune
	next int
}

// Matrix is the animation. The zero value is not usable; call New.
type Matrix struct {
	cols, rows int
	col        []column
	// glyph holds the character shown at each cell. Keeping them lets the
	// trail stay stable between frames instead of reshuffling every glyph
	// every frame, which looks like static rather than falling text.
	glyph []rune
	// changed records the tick each cell last took a new glyph, so the frame it
	// changed on can be drawn differently. See Blend.
	changed []int
	rng     *rand.Rand

	// Glyphs is the alphabet drawn. Replace before the first frame.
	Glyphs []rune

	// Words makes each stream carry a word rather than random glyphs: the
	// head emits the word letter by letter as it falls, so a column reads
	// downward. Empty, the default, is the glyph rain.
	//
	// A word shorter than its trail repeats, separated by WordGap blanks.
	// Words is sampled when a stream starts, so replacing it takes effect on
	// streams that begin after.
	Words []string

	// WordGap is the blank cells between one word in a stream and the next.
	// Zero runs them together, which reads as one long string of letters
	// rather than as two words.
	WordGap int

	// FreshWords draws a new word each time a stream finishes one, so a long
	// column reads as several different words falling rather than as one
	// word said over and over. The zero value repeats the stream's word.
	FreshWords bool
	// Palette runs from the dim tail to the bright head.
	Palette canvas.Palette
	// Density is the chance per simulation step that an idle column starts
	// falling, as a reciprocal: 40 means roughly one in forty.
	Density int
	// StepRate is simulation steps per second. The rain was tuned at one step
	// per frame at 30 fps, so 30 keeps its old speed while the screen redraws
	// more often.
	StepRate float64

	// Hot is the chance a new stream gets a highlighted leading glyph, as a
	// reciprocal: 5 means roughly one in five, which is what the film has.
	Hot int

	// ChangeEvery is how many steps a glyph holds before it may change.
	//
	// In the film a glyph is static for three frames and then turns into
	// another one, and — the part that is easy to miss — every glyph that is
	// going to change changes on the SAME frame. The alphabet does not shimmer
	// continuously; it ticks over, all at once, and is still in between.
	ChangeEvery int

	// StammerEvery is roughly how many steps pass between stammers, which is
	// the oddest behavior in the original: every so often every highlighted
	// glyph hesitates at the same moment, and the streams carrying them drop a
	// row behind the ones that did not. Zero disables it.
	StammerEvery int

	// Blend dims a glyph on the step it changes, out of 256. Zero disables it.
	//
	// In the film a change is a crossfade: "during a single frame, the new and
	// old glyph occupy the same space, each at 50% opacity". A terminal cell
	// holds one glyph and cannot draw two, so the half-lit pair is approximated
	// by the one thing a cell can do — come up dim for that step and reach full
	// brightness on the next.
	//
	// It is what makes the trail pulse. Without it a change is instantaneous and
	// the rain reads as a field of glyphs being swapped; with it each change has
	// a visible beat, and a cell that changes twice in a row appears to waver
	// between two characters, which is what the crossfade looks like.
	Blend int

	// acc carries the fraction of a step left over from the last frame.
	acc float64
	// tick counts simulation steps, for the change cadence and the stammer.
	tick int
	// stammerAt is the tick the next stammer falls on.
	stammerAt int
}

// New returns a matrix animation. seed of 0 gives a fixed sequence, which
// makes tests repeatable.
func New(seed int64) *Matrix {
	return &Matrix{
		rng:      rand.New(rand.NewSource(seed)),
		Glyphs:   Glyphs,
		Palette:  canvas.Matrix,
		Density:  40,
		StepRate: 30,
		// Roughly one stream in five carries a highlighted glyph.
		Hot: 5,
		// Three frames static, then every glyph that is due changes at once.
		ChangeEvery: 3,
		// About every two seconds at the default step rate.
		StammerEvery: 60,
		// Half-lit on the step a glyph changes, which is the crossfade.
		Blend: 128,
	}
}

// Resize allocates per-column state.
func (m *Matrix) Resize(cols, rows int) {
	m.cols, m.rows = cols, rows
	m.col = make([]column, cols)
	m.glyph = make([]rune, cols*rows)
	m.changed = make([]int, cols*rows)
	for i := range m.changed {
		m.changed[i] = -1
	}
	m.seedGlyphs()
	// Stagger the start so the screen does not begin with every column
	// releasing at once.
	for x := range m.col {
		if m.rng.Intn(3) == 0 {
			m.col[x] = m.newColumn(float64(-m.rng.Intn(rows)))
		}
	}
}

// seedGlyphs fills the buffer before any stream exists, so frame one is
// rain rather than an empty screen.
//
// With Words set it seeds whole words down each column rather than loose
// letters. A stream lights cells its head has not reached yet, and a
// column seeded with letters from arbitrary positions would read as a
// word with a jump in it until the head had passed the whole screen.
func (m *Matrix) seedGlyphs() {
	if len(m.Words) == 0 {
		for i := range m.glyph {
			m.glyph[i] = m.randGlyph()
		}
		return
	}
	for x := 0; x < m.cols; x++ {
		w := []rune(m.Words[m.rng.Intn(len(m.Words))])
		if len(w) == 0 {
			continue
		}
		period := len(w) + m.WordGap
		off := m.rng.Intn(period)
		for y := 0; y < m.rows; y++ {
			if i := (y + off) % period; i < len(w) {
				m.glyph[y*m.cols+x] = w[i]
			} else {
				m.glyph[y*m.cols+x] = ' '
			}
		}
	}
}

// nextGlyph is the glyph the head takes as it moves onto a new row: the
// next letter of the stream's word, or a random one when it has none.
func (m *Matrix) nextGlyph(c *column) rune {
	if len(c.word) == 0 {
		return m.randGlyph()
	}
	period := len(c.word) + m.WordGap
	if m.FreshWords && c.next >= period && len(m.Words) > 0 {
		// The stream has finished its word (and its gap). Take another
		// rather than saying the same one again.
		c.word = []rune(m.Words[m.rng.Intn(len(m.Words))])
		c.next = 0
		period = len(c.word) + m.WordGap
	}
	i := c.next % period
	c.next++
	if i >= len(c.word) {
		return ' '
	}
	return c.word[i]
}

func (m *Matrix) randGlyph() rune {
	if len(m.Glyphs) == 0 {
		return ' '
	}
	return m.Glyphs[m.rng.Intn(len(m.Glyphs))]
}

func (m *Matrix) newColumn(head float64) column {
	var word []rune
	if len(m.Words) > 0 {
		word = []rune(m.Words[m.rng.Intn(len(m.Words))])
	}
	return column{
		word:   word,
		head:   head,
		speed:  0.25 + m.rng.Float64()*0.75,
		length: 6 + m.rng.Intn(m.rows),
		active: true,
		hot:    m.Hot > 0 && m.rng.Intn(m.Hot) == 0,
	}
}

// Frame advances every column and repaints.
//
// The simulation is stepped at a fixed rate from elapsed time rather than once
// per frame. Everything here is per-step — the fall distance, the chance a
// column starts, the chance a glyph flickers — so driving it from an
// accumulator keeps all of that meaning exactly what it did, instead of
// needing every probability restated as a rate. The columns land on whole
// cells regardless, so stepping faster than this would buy nothing.
func (m *Matrix) Frame(screen tcell.Screen, cols, rows int, dt float64) {
	if m.cols == 0 || m.rows == 0 {
		return
	}
	m.AdvanceTime(dt)
	m.draw(screen)
}

// AdvanceTime runs however many simulation steps dt seconds are worth, keeping
// the fraction left over for next time.
//
// Advance takes whole steps and is what a still frame wants. This is what an
// animation wants, and it is separate from Frame because not every animation
// has a tcell.Screen to draw on — one composed into a string a frame at a time
// needs the clock without the drawing. See matrix/backdrop.
func (m *Matrix) AdvanceTime(dt float64) {
	rate := m.StepRate
	if rate <= 0 {
		rate = 30
	}
	m.acc += dt * rate
	if m.acc > 4 {
		m.acc = 4 // bound the catch-up after a stall
	}
	for m.acc >= 1 {
		m.step()
		m.acc--
	}
}

// step advances every column by one simulation tick.
func (m *Matrix) step() {
	m.tick++

	// The alphabet turns over on a shared beat rather than each glyph
	// shimmering to its own clock. Everything that is going to change this step
	// changes together, and in between the screen is still — which is what the
	// film does, and what makes it read as text being rewritten rather than as
	// static.
	changing := m.ChangeEvery <= 0 || m.tick%m.ChangeEvery == 0

	// A stammer: every highlighted glyph hesitates on the same step, so the
	// streams carrying them drop a row behind the ones that did not. It is the
	// least obvious thing in the original and the reason the highlighted heads
	// do not stay in formation with the rest of the rain.
	stammer := false
	if m.StammerEvery > 0 {
		if m.stammerAt == 0 {
			m.stammerAt = m.tick + m.StammerEvery/2 + m.rng.Intn(m.StammerEvery)
		}
		if m.tick >= m.stammerAt {
			stammer = true
			m.stammerAt = m.tick + m.StammerEvery/2 + m.rng.Intn(m.StammerEvery)
		}
	}

	for x := 0; x < m.cols; x++ {
		c := &m.col[x]
		if !c.active {
			if m.rng.Intn(m.Density) == 0 {
				*c = m.newColumn(0)
				// The head only writes when it MOVES to a new row, so the row
				// it starts on would otherwise keep whatever the buffer was
				// seeded with. One cell per stream, and with Words set it is
				// a letter from the wrong place in the wrong word.
				m.setGlyph(x, int(c.head), m.nextGlyph(c))
			}
			continue
		}

		if !(stammer && c.hot) {
			prev := int(c.head)
			c.head += c.speed
			// The glyph the head has just moved onto is new, so it is drawn new
			// whatever the beat is doing.
			if int(c.head) != prev {
				m.setGlyph(x, int(c.head), m.nextGlyph(c))
			}
		}

		// One glyph somewhere in the trail turns over, on the beat.
		// One glyph somewhere in the trail turns over, on the beat. A word
		// stream is exempt: swapping a letter mid-fall spells something else.
		if changing && len(c.word) == 0 && m.rng.Intn(3) == 0 {
			m.changeGlyph(x, m.rng.Intn(m.rows), m.randGlyph())
		}

		// Retire the column once its whole trail is off the bottom.
		if int(c.head)-c.length > m.rows {
			c.active = false
		}
	}
}

// setGlyph writes a glyph without marking it as a change.
//
// Used where the head advances into a row. That cell was below the stream and
// unlit, so there is no old glyph to cross-fade from — nothing to dim, and
// dimming it anyway makes the leading glyph flicker instead of lead.
func (m *Matrix) setGlyph(x, y int, r rune) {
	if x < 0 || y < 0 || x >= m.cols || y >= m.rows {
		return
	}
	m.glyph[y*m.cols+x] = r
}

// changeGlyph replaces a glyph that was already lit. That is a cross-fade, so
// the cell is drawn dim for the step it happens on. See Blend.
func (m *Matrix) changeGlyph(x, y int, r rune) {
	if x < 0 || y < 0 || x >= m.cols || y >= m.rows {
		return
	}
	i := y*m.cols + x
	m.glyph[i] = r
	m.changed[i] = m.tick
}

// Cell is one lit cell of a frame: the glyph, how bright it is as an index
// into Palette, and whether it is a highlighted leading glyph.
type Cell struct {
	Rune      rune
	Intensity int
	Hot       bool
}

// cellAt returns what is drawn at x, y, and whether anything is at all.
//
// draw and Cells both go through here so that a still taken for a backdrop and
// the animation on screen cannot drift apart. Everything about how the rain
// looks — the falloff, which heads go white, the crossfade — is decided once,
// in this function.
func (m *Matrix) cellAt(x, y int) (Cell, bool) {
	c := m.col[x]
	if !c.active {
		return Cell{}, false
	}
	d := int(c.head) - y // distance behind the head
	if d < 0 || d >= c.length {
		return Cell{}, false
	}
	// Intensity falls off along the trail, and the leading glyph is the
	// brightest — which is what gives the effect its sense of direction.
	//
	// Only a HIGHLIGHTED stream's head goes white, though, and only about one
	// in five is. The rest lead in the palette's brightest green like the trail
	// behind them. Whitening every head is the obvious reading of "the leading
	// character is bright" and it is wrong: it turns the screen into a row of
	// white dots racing down it, where the film has plain green rain with white
	// heads scattered through it.
	hot := d == 0 && c.hot
	var i int
	switch {
	case hot:
		i = 255
	case d == 0:
		i = 200
	default:
		i = 200 - d*200/c.length
		if i < 0 {
			i = 0
		}
	}
	// The step a glyph changed on is drawn dim: in the film the old and new
	// glyph share the cell at half opacity for one frame, and a cell that can
	// only hold one glyph shows that as a beat instead.
	if m.Blend > 0 && m.changed[y*m.cols+x] == m.tick {
		i = i * m.Blend / 256
	}
	return Cell{Rune: m.glyph[y*m.cols+x], Intensity: i, Hot: hot}, true
}

// Cells reports every lit cell of the current frame, left to right and top to
// bottom. Dark cells are skipped rather than reported blank.
//
// This is the frame without a tcell.Screen to put it on, which is what lets a
// still be composited into ordinary terminal output instead of taking over the
// terminal. See matrix/backdrop.
func (m *Matrix) Cells(fn func(x, y int, c Cell)) {
	for y := 0; y < m.rows; y++ {
		for x := 0; x < m.cols; x++ {
			if c, ok := m.cellAt(x, y); ok {
				fn(x, y, c)
			}
		}
	}
}

// Advance runs n simulation steps without drawing anything.
//
// A still needs this. The rain starts with every column idle, so frame one is
// an empty screen and frame ten is a scattering of single glyphs at the top;
// a backdrop wants rain that has been falling for a while.
func (m *Matrix) Advance(n int) {
	for i := 0; i < n; i++ {
		m.step()
	}
}

// Size returns the grid the animation was last resized to.
func (m *Matrix) Size() (cols, rows int) { return m.cols, m.rows }

func (m *Matrix) draw(screen tcell.Screen) {
	for x := 0; x < m.cols; x++ {
		for y := 0; y < m.rows; y++ {
			c, ok := m.cellAt(x, y)
			if !ok {
				screen.Put(x, y, canvas.Blank, tcell.StyleDefault) //nolint:errcheck // one cell cannot fail
				continue
			}
			st := tcell.StyleDefault.Foreground(m.Palette[c.Intensity])
			if c.Hot {
				st = st.Bold(true)
			}
			canvas.PutRune(screen, x, y, c.Rune, st)
		}
	}
}

// Run draws falling glyphs on the screen until the user quits.
func Run(screen tcell.Screen, seed int64) error {
	return canvas.RunCells(screen, New(seed), canvas.Options{})
}
