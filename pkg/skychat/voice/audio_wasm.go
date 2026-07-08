//go:build js && wasm

// Package voice pkg/skychat/voice/audio_wasm.go c2-app-chat
//
// Browser audio backend for the wasm visor. getUserMedia + AudioContext are
// main-thread-only, but the visor runs in a Web Worker, so the WebAudio graph
// lives in a main-thread JS proxy (voice-audio.js) and audio crosses the worker
// boundary by postMessage — exactly like the WebRTC/STUN proxies (worker.js
// __skywireRTC / __skywireStunIP). This file is the WORKER-side Go glue:
//
//	CAPTURE  main thread mic → {t:'voiceMic'} → worker.js calls __skyvoiceMic(bytes)
//	         → this Source's ring → session sendLoop.
//	PLAYBACK session recvLoop → this Sink → self.__skyvoiceEmit(bytes)
//	         → worker.js posts {t:'voicePlay'} → main thread plays.
//
// In the in-page (single-file) visor, worker.js isn't involved: voice-audio.js
// defines __skyvoiceEmit/__skyvoiceMic on the same global and the calls are
// direct. PCM is 48 kHz mono int16 throughout.
package voice

import (
	"sync"
	"syscall/js"
)

var (
	audioMu    sync.Mutex
	curMicRing *sampleRing // current call's capture ring (bridge → Source)
	bridgeOnce sync.Once
)

// installBridge registers __skyvoiceMic — the function the capture side calls to
// deliver one mic frame (LE int16 bytes) to the current call's Source.
func installBridge() {
	js.Global().Set("__skyvoiceMic", js.FuncOf(func(_ js.Value, args []js.Value) any {
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
}

// wasmSource is a voice.Source fed by mic frames the capture proxy delivers.
type wasmSource struct{ ring *sampleRing }

// NewMicSource returns a browser capture Source. monitor is ignored (browsers
// can't capture arbitrary system audio); rate is fixed at the package rate.
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

// wasmSink is a voice.Sink that pushes each decoded frame to the playback proxy
// via the __skyvoiceEmit hook (defined by worker.js in worker mode, or
// voice-audio.js in-page).
type wasmSink struct{}

// NewSpeakerSink returns a browser playback Sink.
func NewSpeakerSink(_ int) (Sink, error) {
	bridgeOnce.Do(installBridge)
	return &wasmSink{}, nil
}

func (s *wasmSink) Write(pcm []int16) (int, error) {
	emit := js.Global().Get("__skyvoiceEmit")
	if emit.Type() != js.TypeFunction {
		return len(pcm), nil // no playback sink wired — drop
	}
	buf := make([]byte, len(pcm)*2)
	for i, v := range pcm {
		buf[i*2] = byte(v)
		buf[i*2+1] = byte(uint16(v) >> 8) //nolint:gosec // LE int16 packing
	}
	u8 := js.Global().Get("Uint8Array").New(len(buf))
	js.CopyBytesToJS(u8, buf)
	emit.Invoke(u8)
	return len(pcm), nil
}

func (s *wasmSink) Close() error { return nil }
