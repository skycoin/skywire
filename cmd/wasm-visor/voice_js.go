//go:build js && wasm

// Package main cmd/wasm-visor/voice_js.go c3-vis-wasm
//
// Wires pkg/skychat/voice into the wasm visor over the SAME app.Client the
// browser skychat already uses: signaling (invite/accept/decline/hangup) on
// skyenv.SkychatVoiceSignalPort over dmsg + skynet, media (RTP) on the same
// conn. The codec is the pure-Go Opus (compiles under js/wasm — no WebCodecs
// needed); audio I/O is the browser WebAudio backend (pkg/skychat/voice/
// audio_wasm.go) driven by a main-thread proxy, since getUserMedia/AudioContext
// aren't available in the worker where the visor runs.
//
// Explicit-answer only: an inbound call RINGS (skychatVoiceIncoming lists it)
// and the page answers with skychatVoiceAnswer — a tab never streams its mic
// without the user answering.
package main

import (
	"context"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"syscall/js"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	skyvoice "github.com/skycoin/skywire/pkg/skychat/voice"
	"github.com/skycoin/skywire/pkg/skyenv"
)

var voiceMgr *skyvoice.Manager

// voiceDialWasm opens a signaling/media stream to peer on the voice port,
// dmsg-first then skynet, via the browser skychat app.Client.
func voiceDialWasm(_ context.Context, peer cipher.PubKey, port uint16) (net.Conn, error) {
	if chatClient == nil {
		return nil, fmt.Errorf("skychat not started yet")
	}
	var lastErr error
	for _, n := range []appnet.Type{appnet.TypeDmsg, appnet.TypeSkynet} {
		conn, err := chatClient.Dial(appnet.Addr{Net: n, PubKey: peer, Port: routing.Port(port)})
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no network available")
	}
	return nil, lastErr
}

// startVoiceWasm builds the voice manager on the browser skychat's app.Client
// and listens for inbound calls on the voice port over dmsg + skynet.
func startVoiceWasm(ctx context.Context, cl *app.Client) {
	cfg := skyvoice.Config{
		LocalPK:      cl.Config().VisorPK,
		Dial:         voiceDialWasm,
		SignalPort:   skyenv.SkychatVoiceSignalPort,
		ManualAnswer: true, // ring; answer explicitly (never auto-stream the mic)
		Visualize:    true,
		NewCodec: func() skyvoice.Codec {
			c, err := skyvoice.NewOpusCodec()
			if err != nil {
				return skyvoice.NewPCMCodec()
			}
			return c
		},
		NewSource: func() skyvoice.Source {
			s, err := skyvoice.NewMicSource(false, 0)
			if err != nil {
				return skyvoice.SilentSource{}
			}
			return s
		},
		NewSink: func() skyvoice.Sink {
			s, err := skyvoice.NewSpeakerSink(0)
			if err != nil {
				return skyvoice.NullSink{}
			}
			return s
		},
		Ring: func(inv skyvoice.Sig) {
			vlog(fmt.Sprintf("voice: incoming call from %s — answer to accept", shortPK(inv.FromPK.Hex())))
		},
	}
	voiceMgr = skyvoice.NewManager(cfg)

	var listeners []net.Listener
	for _, n := range []appnet.Type{appnet.TypeDmsg, appnet.TypeSkynet} {
		lis, err := cl.Listen(n, routing.Port(skyenv.SkychatVoiceSignalPort))
		if err != nil {
			vlog(fmt.Sprintf("voice: listen %s:%d: %s", n, skyenv.SkychatVoiceSignalPort, err.Error()))
			continue
		}
		vlog(fmt.Sprintf("voice: listening on %s:%d", n, skyenv.SkychatVoiceSignalPort))
		listeners = append(listeners, lis)
	}
	if len(listeners) == 0 {
		vlog("voice: no listeners started")
		return
	}
	go voiceMgr.Serve(ctx, listeners...)
}

// jsSkychatVoiceCall(peerPkHex) → Promise<{id}>. Blocks until the callee answers.
func jsSkychatVoiceCall(_ js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return js.Global().Get("Error").New("skychatVoiceCall(peerPkHex)")
	}
	pkHex := args[0].String()
	return promise(func() (interface{}, error) {
		if voiceMgr == nil {
			return nil, fmt.Errorf("voice not started")
		}
		var pk cipher.PubKey
		if err := pk.Set(pkHex); err != nil {
			return nil, fmt.Errorf("bad peer pk: %w", err)
		}
		sess, err := voiceMgr.Call(context.Background(), pk)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"id": sess.CallID}, nil
	})
}

// jsSkychatVoiceAnswer(callID) / Decline / Hangup → Promise<null>.
func jsSkychatVoiceAnswer(_ js.Value, args []js.Value) interface{} {
	return voiceAct(args, func(id string) error { return voiceMgr.Answer(id) })
}

func jsSkychatVoiceDecline(_ js.Value, args []js.Value) interface{} {
	return voiceAct(args, func(id string) error { return voiceMgr.Decline(id) })
}

func jsSkychatVoiceHangup(_ js.Value, args []js.Value) interface{} {
	return voiceAct(args, func(id string) error { return voiceMgr.Hangup(id) })
}

// jsSkychatVoiceMute(callID, mic, speaker) → Promise<null>. mic mutes what the
// peer hears from us; speaker ("mute the caller") mutes what we hear.
func jsSkychatVoiceMute(_ js.Value, args []js.Value) interface{} {
	if len(args) < 3 {
		return js.Global().Get("Error").New("expected callID, mic, speaker")
	}
	id, mic, spk := args[0].String(), args[1].Bool(), args[2].Bool()
	return promise(func() (interface{}, error) {
		if voiceMgr == nil {
			return nil, fmt.Errorf("voice not started")
		}
		return nil, voiceMgr.SetMute(id, mic, spk)
	})
}

func voiceAct(args []js.Value, fn func(id string) error) interface{} {
	if len(args) < 1 {
		return js.Global().Get("Error").New("expected callID")
	}
	id := args[0].String()
	return promise(func() (interface{}, error) {
		if voiceMgr == nil {
			return nil, fmt.Errorf("voice not started")
		}
		return nil, fn(id)
	})
}

// jsSkychatVoiceIncoming() → JSON [{id, from}] of ringing calls.
func jsSkychatVoiceIncoming(js.Value, []js.Value) interface{} {
	if voiceMgr == nil {
		return "[]"
	}
	out := "["
	for i, inv := range voiceMgr.Incoming() {
		if i > 0 {
			out += ","
		}
		out += fmt.Sprintf(`{"id":%q,"from":%q}`, inv.CallID, inv.FromPK.Hex())
	}
	return out + "]"
}

// jsSkychatVoiceLevels(callID) → JSON {sent, recv} current RMS levels (0..1) for
// the live level meter.
func jsSkychatVoiceLevels(_ js.Value, args []js.Value) interface{} {
	if voiceMgr == nil || len(args) < 1 {
		return `{"sent":0,"recv":0}`
	}
	sent, recv, err := voiceMgr.CallAudio(args[0].String())
	if err != nil {
		return `{"sent":0,"recv":0}`
	}
	return fmt.Sprintf(`{"sent":%.4f,"recv":%.4f}`, rmsLevel(sent), rmsLevel(recv))
}

// jsSkychatVoiceCallAudio(callID) → JSON {sent:[],recv:[]} recent PCM for the
// spectrogram (only polled while the spectrogram view is open).
func jsSkychatVoiceCallAudio(_ js.Value, args []js.Value) interface{} {
	if voiceMgr == nil || len(args) < 1 {
		return `{"sent":[],"recv":[]}`
	}
	sent, recv, err := voiceMgr.CallAudio(args[0].String())
	if err != nil {
		return `{"sent":[],"recv":[]}`
	}
	var b strings.Builder
	b.WriteString(`{"sent":`)
	writeInt16Array(&b, sent)
	b.WriteString(`,"recv":`)
	writeInt16Array(&b, recv)
	b.WriteString("}")
	return b.String()
}

func writeInt16Array(b *strings.Builder, a []int16) {
	b.WriteByte('[')
	for i, s := range a {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(int(s)))
	}
	b.WriteByte(']')
}

// rmsLevel: RMS of the last ~0.1s of pcm, normalized 0..1.
func rmsLevel(pcm []int16) float64 {
	if len(pcm) == 0 {
		return 0
	}
	const window = 4800
	if len(pcm) > window {
		pcm = pcm[len(pcm)-window:]
	}
	var sum float64
	for _, s := range pcm {
		f := float64(s) / 32768.0
		sum += f * f
	}
	return math.Sqrt(sum / float64(len(pcm)))
}

// jsSkychatVoiceActive() → JSON [callID] of active calls.
func jsSkychatVoiceActive(js.Value, []js.Value) interface{} {
	if voiceMgr == nil {
		return "[]"
	}
	out := "["
	for i, id := range voiceMgr.Active() {
		if i > 0 {
			out += ","
		}
		out += fmt.Sprintf("%q", id)
	}
	return out + "]"
}
