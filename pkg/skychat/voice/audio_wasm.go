//go:build js && wasm

// Package voice pkg/skychat/voice/audio_wasm.go c2-app-chat
//
// Browser audio backend for the wasm visor. getUserMedia + AudioContext are
// main-thread-only, but the wasm visor runs in a Web Worker, so the actual
// WebAudio graph lives in a main-thread JS proxy (like the WebRTC proxy). This
// file is only the WORKER-side Go glue: a Source whose ring is fed by mic frames
// the proxy delivers, and a Sink whose ring the proxy drains for playback. The
// bridge is three functions the proxy calls on globalThis:
//
//	__skyvoiceMic(Uint8Array)  — deliver a captured frame (LE int16 bytes) → Source
//	__skyvoicePlay(nSamples)   — pull nSamples LE int16 bytes for playback ← Sink
//	                             (returns a Uint8Array; short/silent on underrun)
//	globalThis.__skyvoiceActive(bool) — the proxy checks this to start/stop capture
//
// PCM everywhere is 48 kHz mono int16, matching the rest of pkg/skychat/voice.
package voice

import (
	"sync"
	"syscall/js"
)

var (
	audioMu    sync.Mutex
	curMicRing *sampleRing // current call's capture ring (proxy → Source)
	curSpkRing *sampleRing // current call's playback ring (Sink → proxy)
	bridgeOnce sync.Once
)

// installBridge registers the JS functions the main-thread audio proxy calls.
func installBridge() {
	g := js.Global()
	g.Set("__skyvoiceMic", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) < 1 {
			return nil
		}
		u8 := args[0]
		n := u8.Get("length").Int()
		buf := make([]byte, n)
		js.CopyBytesToGo(buf, u8)
		pcm := make([]int16, n/2)
		for i := range pcm {
			pcm[i] = int16(uint16(buf[i*2]) | uint16(buf[i*2+1])<<8) //nolint:gosec // LE int16 reassembly
		}
		audioMu.Lock()
		r := curMicRing
		audioMu.Unlock()
		if r != nil {
			r.push(pcm)
		}
		return nil
	}))
	g.Set("__skyvoicePlay", js.FuncOf(func(_ js.Value, args []js.Value) any {
		n := 0
		if len(args) >= 1 {
			n = args[0].Int()
		}
		audioMu.Lock()
		r := curSpkRing
		audioMu.Unlock()
		pcm := make([]int16, n)
		if r != nil {
			r.popSilence(pcm)
		}
		buf := make([]byte, n*2)
		for i, s := range pcm {
			buf[i*2] = byte(s)
			buf[i*2+1] = byte(uint16(s) >> 8) //nolint:gosec // LE int16 packing
		}
		out := js.Global().Get("Uint8Array").New(len(buf))
		js.CopyBytesToJS(out, buf)
		return out
	}))
	g.Set("__skyvoiceActive", js.FuncOf(func(_ js.Value, _ []js.Value) any {
		audioMu.Lock()
		active := curMicRing != nil || curSpkRing != nil
		audioMu.Unlock()
		return active
	}))
}

// wasmSource is a voice.Source fed by mic frames from the main-thread proxy.
type wasmSource struct{ ring *sampleRing }

// NewMicSource returns a browser capture Source. monitor is ignored (browsers
// can't capture arbitrary system audio). rate is fixed at the package rate.
func NewMicSource(_ bool, _ int) (Source, error) {
	bridgeOnce.Do(installBridge)
	ring := newSampleRing(sampleRate)
	audioMu.Lock()
	curMicRing = ring
	audioMu.Unlock()
	return &wasmSource{ring: ring}, nil
}

func (s *wasmSource) Read(pcm []int16) (int, error) { return s.ring.popBlocking(pcm), nil }

func (s *wasmSource) Close() error {
	s.ring.close()
	audioMu.Lock()
	if curMicRing == s.ring {
		curMicRing = nil
	}
	audioMu.Unlock()
	return nil
}

// wasmSink is a voice.Sink whose ring the main-thread proxy drains for playback.
type wasmSink struct{ ring *sampleRing }

// NewSpeakerSink returns a browser playback Sink.
func NewSpeakerSink(_ int) (Sink, error) {
	bridgeOnce.Do(installBridge)
	ring := newSampleRing(sampleRate)
	audioMu.Lock()
	curSpkRing = ring
	audioMu.Unlock()
	return &wasmSink{ring: ring}, nil
}

func (s *wasmSink) Write(pcm []int16) (int, error) {
	s.ring.push(pcm)
	return len(pcm), nil
}

func (s *wasmSink) Close() error {
	audioMu.Lock()
	if curSpkRing == s.ring {
		curSpkRing = nil
	}
	audioMu.Unlock()
	return nil
}
