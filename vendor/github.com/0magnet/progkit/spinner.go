package progkit

// Spinner is a one-cell activity indicator. Tick it on a timer and draw
// Frame where it goes.
type Spinner struct{ i int }

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Tick moves the spinner on.
func (s *Spinner) Tick() { s.i = (s.i + 1) % len(spinnerFrames) }

// Frame is the spinner's current glyph.
func (s *Spinner) Frame() string { return spinnerFrames[s.i] }
