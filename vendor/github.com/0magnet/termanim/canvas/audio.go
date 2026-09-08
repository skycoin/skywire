package canvas

import "math"

// This file is the seam between sound and animation.
//
// The effects in this repository are driven by time alone, which is why they
// drift rather than dance. Given the loudness of whatever is playing they
// become a visualizer, and the same animation then serves a terminal, a
// browser tab and a music player without being three programs.
//
// Nothing here transports audio. What arrives is already a number: a host
// measures the sound however it can — PulseAudio behind a socket, the Web
// Audio API in a page, a file being decoded — and hands one small value in
// per frame. That keeps the platform-specific half out of a library that has
// to compile for wasm as well as for a terminal.
//
// The governing constraint is that adding this changed nothing. An animation
// with no source attached must draw exactly what it drew before, pixel for
// pixel, because other things already depend on these packages. Every mapping
// below is therefore written so that a level of zero is an arithmetic
// identity — a multiply by one or an add of zero — rather than merely a small
// number.

// Bands is how many frequency bands an Audio carries.
//
// Eight, because it is the fewest that still separates a kick from a snare
// from a hi-hat, and because a fixed-size array of them fits in the value and
// so costs no allocation. A host with a finer analysis averages down into
// these; a host with none at all leaves them zero and Band falls back to the
// overall level.
const Bands = 8

// Audio is the sound of one frame.
//
// The zero value is silence, and that is load-bearing rather than
// convenient: it is what lets an animation hold one of these unconditionally
// and lets a host that has not connected yet supply nothing.
//
// It is a value type with no pointers and no slice in it, so passing one per
// frame allocates nothing — the same reason animations allocate in Resize and
// not in Frame.
type Audio struct {
	// Level is the overall loudness, 0 for silence and 1 for as loud as the
	// host is willing to report. It is deliberately not decibels and not a
	// sample count: an animation should not have to know how the host
	// measured, only how hard to push.
	Level float64
	// Spectrum splits that loudness across Bands, lowest frequency first.
	// All zero means the host has no spectrum, which is a normal thing for a
	// host to be: see Band.
	Spectrum [Bands]float64
}

// Band returns the level of band i, clamped to the range of the spectrum.
//
// When the spectrum is entirely zero the overall level is returned instead.
// That fallback exists so a host with nothing but an amplitude — a VU meter, a
// peak from a socket — still drives the animations that ask for bands, rather
// than those animations quietly sitting still while the level-driven ones
// move. Silence is unaffected, because a silent Audio has a zero level too.
func (a Audio) Band(i int) float64 {
	if !a.hasSpectrum() {
		return a.Level
	}
	if i < 0 {
		i = 0
	} else if i >= Bands {
		i = Bands - 1
	}
	return a.Spectrum[i]
}

// hasSpectrum reports whether any band carries anything. Eight comparisons,
// done once per frame in Envelope.Step rather than once per band.
func (a Audio) hasSpectrum() bool {
	for _, v := range a.Spectrum {
		if v != 0 {
			return true
		}
	}
	return false
}

// AudioSource is asked for the sound of the frame about to be drawn.
//
// It is a pull and not a push. The frame loop already owns the clock, and
// asking at frame time gets the newest measurement and discards the rest,
// which is what you want: a channel would either block the loop or build a
// backlog of sound that has already been heard, and a visualizer running a
// second behind the music is worse than one that dropped it.
//
// dt is the seconds since the previous frame — the same value the animation
// is about to be given. A source that measures ignores it; a source that
// generates, like Beat, advances on it and is then exactly as frame-rate
// independent as everything else here, and repeatable in a test with no sound
// card in the machine.
type AudioSource func(dt float64) Audio

// Steady returns a source that reports the same sound every frame. Steady of
// the zero Audio is silence, which is the source you attach to prove that
// attaching one changed nothing.
func Steady(a Audio) AudioSource {
	return func(float64) Audio { return a }
}

// Beat returns a source that thumps at bpm beats a minute, for testing the
// wiring and for anyone who wants to watch it without playing music.
//
// The shape is percussive: every beat starts at full and decays away, because
// a sound that ramps up has no attack for an envelope to catch and would
// exercise none of the smoothing below. Higher bands ring shorter than lower
// ones — a hi-hat dies before a kick does — so the bands are visibly
// different from one another rather than one signal copied eight times, which
// is what an animation driven by bands needs in order to look driven by them.
//
// Each call returns a source with its own phase starting at zero, so a test
// that constructs one gets the same sequence every time.
func Beat(bpm float64) AudioSource {
	if bpm <= 0 {
		bpm = 120
	}
	period := 60 / bpm
	var t float64
	return func(dt float64) Audio {
		t += dt
		// A loop rather than a modulus: dt is a frame, the period is most of
		// a second, so this runs once and never divides.
		for t >= period {
			t -= period
		}
		var a Audio
		a.Level = math.Exp(-t * 12)
		for i := range a.Spectrum {
			a.Spectrum[i] = math.Exp(-t * (8 + 4*float64(i)))
		}
		return a
	}
}

// AudioListener is implemented by an animation that reacts to sound. Run
// checks for it and, if a source is attached, calls Listen once per frame
// immediately before Frame.
//
// An optional interface rather than a change to Animation, because Animation
// is implemented by every effect in this repository and by anything outside
// it that embeds one. Widening Frame to take an Audio would have broken all
// of them to serve four, and would have forced the other twenty-odd to
// mention a parameter they ignore. The cost of the choice is one type
// assertion per Run — paid once, at setup, not per frame — and that the
// coupling is invisible to the compiler: an animation that means to react but
// misspells the method silently does not, which is what the "silence is
// identical" tests are really guarding from the other side.
type AudioListener interface {
	// Listen delivers the sound of the frame about to be drawn. It is called
	// before Frame, never concurrently with it, and only when a source is
	// attached — so an animation that is never listened to must behave
	// exactly as though this method did not exist.
	Listen(a Audio)
}

// Default attack and decay for an Envelope, in seconds.
//
// Attack is a little longer than a frame rather than shorter. Shorter is
// tempting — an onset ought to be seen on the frame it happens — but a time
// constant under a frame means the envelope follows whatever single sample
// turned up, and one bad sample then throws the display to full and back,
// which is precisely the shivering this type exists to stop. 40 ms puts about
// 57% of a step into the first frame at 30fps and 34% at 60, so a real onset
// is unmistakable within two frames while a lone spike never gets there.
//
// Decay is much longer, because the ear hears a drum as a hit and a tail, and
// an animation that fell back the instant the sample did would flicker at the
// sample rate rather than pulse at the beat. 250 ms is about the gap between
// sixteenth notes at 120bpm, which is the fastest thing worth resolving.
//
// Attack shorter than decay is the whole shape: rise with the transient, ease
// out after it. Equal constants give a smoothed amplitude, which is a
// perfectly faithful picture of the sound and reads as nothing at all.
const (
	DefaultAttack = 0.040
	DefaultDecay  = 0.250
)

// Envelope smooths a stream of Audio into something worth looking at.
//
// A raw amplitude is not musical. It is a jagged thing that jumps between
// frames even during a held note, and driving a picture with it directly
// gives a picture that shivers. What reads as rhythm is a fast rise and a slow
// fall: the display leaps on the transient and eases back down, which is what
// a needle on a meter does and what the ear expects to see.
//
// The zero value works and means silence with the default constants. Set
// Attack and Decay before the first Step to taste — each animation here picks
// its own, because a flame and a tunnel do not want the same tail.
type Envelope struct {
	// Attack is the time constant for a rising signal, in seconds. Zero means
	// DefaultAttack.
	Attack float64
	// Decay is the time constant for a falling one. Zero means DefaultDecay.
	Decay float64

	level float64
	bands [Bands]float64
}

// Step advances the envelope by dt seconds towards the given sound.
//
// The coefficient is 1-exp(-dt/tau) rather than a fixed fraction per frame,
// and that is the whole reason this is not three lines inline in each
// animation. A fixed fraction makes the smoothing depend on the frame rate:
// the same second of music would decay twice as far at 60fps as at 30, so the
// visualizer would look different on a fast machine. With the exponential
// form the state after a given interval is the same however that interval was
// divided into frames, which is the same promise dt makes everywhere else in
// this package.
//
// Silence leaves the envelope at exactly zero: the update is
// cur + (target-cur)*k, and with both at zero that is an exact zero however
// many times it runs. That is what makes an attached silent source
// indistinguishable from no source at all, rather than merely close to it.
func (e *Envelope) Step(a Audio, dt float64) {
	if dt <= 0 {
		return
	}
	attack, decay := e.Attack, e.Decay
	if attack <= 0 {
		attack = DefaultAttack
	}
	if decay <= 0 {
		decay = DefaultDecay
	}
	// Two exponentials per frame, not per band: the coefficient depends only
	// on the direction of travel, and there are only two directions.
	ka := 1 - math.Exp(-dt/attack)
	kd := 1 - math.Exp(-dt/decay)

	e.level = follow(e.level, a.Level, ka, kd)
	// Resolved once rather than inside Band per band, and the result decides
	// what the whole spectrum follows.
	spec := a.hasSpectrum()
	for i := range e.bands {
		v := a.Level
		if spec {
			v = a.Spectrum[i]
		}
		e.bands[i] = follow(e.bands[i], v, ka, kd)
	}
}

// Level is the smoothed overall loudness, 0..1.
func (e *Envelope) Level() float64 { return e.level }

// Band is the smoothed level of band i, 0..1, clamped to the range.
func (e *Envelope) Band(i int) float64 {
	if i < 0 {
		i = 0
	} else if i >= Bands {
		i = Bands - 1
	}
	return e.bands[i]
}

// Reset returns the envelope to silence without disturbing its constants.
// A host swapping sources mid-run wants this; otherwise the tail of the old
// signal bleeds into the new one.
func (e *Envelope) Reset() {
	e.level = 0
	for i := range e.bands {
		e.bands[i] = 0
	}
}

// follow moves cur one step towards target, rising at ka and falling at kd.
//
// The target is clamped here rather than at the animations, because a host
// that hands over a raw peak can overshoot 1 and every mapping downstream
// multiplies by this: one bad sample would otherwise become a frame of
// nonsense in four different effects.
func follow(cur, target, ka, kd float64) float64 {
	if target < 0 {
		target = 0
	} else if target > 1 {
		target = 1
	}
	k := kd
	if target > cur {
		k = ka
	}
	return cur + (target-cur)*k
}

// audioTap returns the function that feeds one frame of sound to a, or nil
// when there is no source or a does not listen.
//
// Returning nil rather than a no-op closure is deliberate: the loop then does
// a nil check instead of an indirect call, so an animation that ignores audio
// costs one comparison a frame and nothing else.
func audioTap(a any, src AudioSource) func(dt float64) {
	if src == nil {
		return nil
	}
	ear, ok := a.(AudioListener)
	if !ok {
		return nil
	}
	return func(dt float64) { ear.Listen(src(dt)) }
}
