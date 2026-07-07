// Package cliskychat cmd/skywire-cli/commands/skychat/voice_spectrogram.go c4-vis-cli
//
// `skywire-cli skychat voice spectrogram` renders a live audio spectrogram in
// the terminal — the CLI-side "see the audio" view for voice chat (stands in for
// video). It captures local audio via the pure-Go PulseAudio backend (mic, or
// --monitor for whatever the system is playing, no mic needed) and feeds it to
// the vendored audioprism DSP core. Linux only; press q / Esc / Ctrl-C to quit.
//
// The rendering deliberately MIRRORS the operator's audioprism-go tcell UI
// (github.com/0magnet/audioprism-go/pkg/ui/tcell) so this view looks the same:
// capture at spectrogram.SampleRate, overlap-stepped FFT columns (one per
// StepSize samples), a fixed 2048×1024 circular history downscaled to the
// terminal, the vertical axis mapped linearly to 0–12 kHz with low frequencies
// at the bottom, and the same MagnitudeToPixel colormap. The same renderer feeds
// the two-panel sent/received view during an actual call (follow-up).
package cliskychat

import (
	"image/color"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	skyvoice "github.com/skycoin/skywire/pkg/skychat/voice"
	"github.com/skycoin/skywire/pkg/skychat/voice/spectrogram"
)

// Fixed history resolution (independent of terminal size), matching audioprism.
const (
	specBufferWidth  = 2048
	specBufferHeight = 1024
	specMaxFreqHz    = 12000 // vertical axis spans 0..12 kHz, like audioprism
)

var voiceSpectrogramMonitor bool

func init() {
	voiceSpectrogramCmd.Flags().BoolVar(&voiceSpectrogramMonitor, "monitor", false,
		"capture the system output (default sink monitor) instead of the microphone — visualize whatever audio is playing, no mic needed")
	voiceCmd.AddCommand(voiceSpectrogramCmd)
}

var voiceSpectrogramCmd = &cobra.Command{
	Use:   "spectrogram",
	Short: "Live audio spectrogram in the terminal (mic, or --monitor for system audio)",
	Long: `Render a live audio spectrogram in the terminal.

Captures local audio (the microphone by default, or the system output with
--monitor) and draws a scrolling spectrogram — the CLI stand-in for a video
feed on a voice call. Matches the audioprism-go tcell view. Linux only
(PulseAudio/PipeWire). Press q / Esc / Ctrl-C to quit.`,
	Run: func(cmd *cobra.Command, _ []string) {
		// Capture at the spectrogram's native rate so the frequency mapping and
		// magnitude scaling match audioprism-go exactly.
		src, err := skyvoice.NewMicSource(voiceSpectrogramMonitor, spectrogram.SampleRate)
		if err != nil {
			cliutil.PrintFatalError(cmd.Flags(), err)
		}
		defer func() { _ = closeSource(src) }() //nolint:errcheck
		if err := runSpectrogramTUI(src); err != nil {
			cliutil.PrintFatalError(cmd.Flags(), err)
		}
	},
}

// specView holds the scrolling spectrogram state (audioprism's model).
type specView struct {
	mu      sync.RWMutex
	history [][]color.Color // [specBufferWidth][specBufferHeight] circular buffer
	head    int             // next write index
	queueMu sync.Mutex
	queue   [][]color.Color // columns computed but not yet folded into history
	overlap []float32       // sliding FFT window
	pending []float32       // samples not yet stepped through
	dftSize int
}

func newSpecView() *specView {
	v := &specView{dftSize: spectrogram.S.GetDFTSize()}
	v.overlap = make([]float32, v.dftSize)
	v.history = make([][]color.Color, specBufferWidth)
	for i := range v.history {
		v.history[i] = make([]color.Color, specBufferHeight)
		for j := range v.history[i] {
			v.history[i][j] = color.Black
		}
	}
	return v
}

// push accumulates captured samples and emits a spectrogram column every
// StepSize samples (overlap-stepped FFT), matching audioprism's processAudioThread.
func (v *specView) push(samples []float32) {
	v.pending = append(v.pending, samples...)
	step := spectrogram.S.StepSize()
	if step <= 0 {
		step = v.dftSize
	}
	for len(v.pending) >= step && len(v.pending) >= v.dftSize-step {
		// Slide the FFT window: shift left by step, append the next samples —
		// exactly audioprism-go's processAudioThread overlap step.
		copy(v.overlap, v.overlap[step:])
		copy(v.overlap[step:], v.pending[:v.dftSize-step])
		v.pending = v.pending[step:]

		mags := spectrogram.ComputeFFT(v.overlap)
		col := make([]color.Color, specBufferHeight)
		for y := 0; y < specBufferHeight; y++ {
			freq := float64(y) / float64(specBufferHeight) * specMaxFreqHz
			bin := int(freq * float64(v.dftSize) / float64(spectrogram.SampleRate))
			if bin < len(mags) {
				col[y] = spectrogram.MagnitudeToPixel(mags[bin])
			} else {
				col[y] = color.Black
			}
		}
		v.queueMu.Lock()
		v.queue = append(v.queue, col)
		if len(v.queue) > 200 {
			v.queue = v.queue[len(v.queue)-100:]
		}
		v.queueMu.Unlock()
	}
}

// drainInto folds all queued columns into the circular history.
func (v *specView) drainInto() {
	v.queueMu.Lock()
	q := v.queue
	v.queue = nil
	v.queueMu.Unlock()
	if len(q) == 0 {
		return
	}
	v.mu.Lock()
	for _, col := range q {
		v.history[v.head] = col
		v.head = (v.head + 1) % specBufferWidth
	}
	v.mu.Unlock()
}

// draw renders the most recent w columns, scaling specBufferHeight to h with low
// frequencies at the bottom (audioprism's drawSpectrogram).
func (v *specView) draw(screen tcell.Screen, w, h int) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	start := (v.head - w + specBufferWidth) % specBufferWidth
	for x := 0; x < w; x++ {
		idx := (start + x) % specBufferWidth
		for y := 0; y < h; y++ {
			by := int(float64(y) / float64(h) * float64(specBufferHeight))
			if by >= specBufferHeight {
				continue
			}
			r, g, b, _ := v.history[idx][by].RGBA()
			style := tcell.StyleDefault.Background(tcell.NewRGBColor(int32(r>>8), int32(g>>8), int32(b>>8)))
			screen.SetContent(x, h-1-y, ' ', nil, style)
		}
	}
}

// runSpectrogramTUI drives the tcell screen: an audio goroutine steps FFT columns
// into a queue; the render loop (~60fps) folds them into history and repaints.
func runSpectrogramTUI(src skyvoice.Source) error {
	screen, err := tcell.NewScreen()
	if err != nil {
		return err
	}
	if err := screen.Init(); err != nil {
		return err
	}
	defer screen.Fini()
	screen.Clear()

	view := newSpecView()
	quit := make(chan struct{})

	// Audio reader → step columns.
	go func() {
		frame := make([]int16, spectrogram.S.StepSize())
		fbuf := make([]float32, len(frame))
		for {
			select {
			case <-quit:
				return
			default:
			}
			n, rerr := src.Read(frame)
			if rerr != nil {
				return
			}
			for i := 0; i < n; i++ {
				fbuf[i] = float32(frame[i]) / 32768.0
			}
			view.push(fbuf[:n])
		}
	}()

	// Event reader.
	go func() {
		for {
			switch ev := screen.PollEvent().(type) {
			case *tcell.EventKey:
				if ev.Key() == tcell.KeyEscape || ev.Key() == tcell.KeyCtrlC ||
					ev.Rune() == 'q' || ev.Rune() == 'Q' {
					close(quit)
					return
				}
			case *tcell.EventResize:
				screen.Sync()
			}
		}
	}()

	ticker := time.NewTicker(time.Second / 60)
	defer ticker.Stop()
	for {
		select {
		case <-quit:
			return nil
		case <-ticker.C:
		}
		w, h := screen.Size()
		if w <= 0 || h <= 1 {
			continue
		}
		specH := h - 1 // bottom row = hint
		view.drainInto()
		screen.Clear()
		view.draw(screen, w, specH)
		drawHint(screen, h-1, w, voiceSpectrogramMonitor)
		screen.Show()
	}
}

func closeSource(src skyvoice.Source) error {
	if c, ok := src.(interface{ Close() error }); ok {
		return c.Close()
	}
	return nil
}

func drawHint(screen tcell.Screen, y, w int, monitor bool) {
	src := "mic"
	if monitor {
		src = "system audio (monitor)"
	}
	hint := []rune(" skychat voice spectrogram — source: " + src + " — q/Esc to quit ")
	st := tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorBlack)
	for x := 0; x < w; x++ {
		r := ' '
		if x < len(hint) {
			r = hint[x]
		}
		screen.SetContent(x, y, r, nil, st)
	}
}
