// Package visor pkg/visor/hypervisor_handlers_voice_audio.go c3-vis-core
//
// The two routes that carry a call's AUDIO between the visor and the app that
// started it, for a visor whose audio device belongs to that app rather than to
// itself — Android, where nothing else can work (see pkg/skychat/call/bridge.go
// and voiceAudioMode in init_voice.go).
//
//	POST /skychat/voice/mic      the app's microphone, streamed in
//	GET  /skychat/voice/speaker  what the calls want played, streamed out
//
// Both carry RAW little-endian int16 PCM, mono, at call.SampleRate — no
// framing, no JSON, no base64. A voice stream is a continuous 96 KB/s in each
// direction and every wrapper here would be paid 50 times a second for nothing;
// the format is fixed on both sides at compile time, so there is nothing to
// negotiate. The one real cost of that choice is that a wrong sample rate is
// silent corruption rather than an error, which is why the constant is exported
// for the app to build its capture and playback from.
//
// **Long-lived, and deliberately so.** A call is minutes; a request per frame
// would be 50 round trips a second in each direction. Each stream instead
// re-arms its own deadline as it goes, exactly as the notification SSE stream
// does — the server's 5 s read / 10 s write timeouts are single deadlines for a
// whole request, and clearing them outright would leave a wedged peer parked in
// a syscall forever holding the device.
//
// LOCAL visor only. The bridge is this process's audio device; proxying it to a
// remote visor would mean shipping someone else's microphone across the mesh
// through the operator's browser, which is not a thing this route offers.
package visor

import (
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/skycoin/skywire/pkg/httputil"
	skycall "github.com/skycoin/skywire/pkg/skychat/call"
)

// voiceAudioIOTimeout bounds ONE read or write on an audio stream before it is
// re-armed. Generous next to the 20 ms frame cadence — it is a liveness bound,
// not a pacing one: it exists so a peer that stops reading (a dozing phone, a
// zero-window receiver) eventually errors out instead of parking the handler.
const voiceAudioIOTimeout = 30 * time.Second

// voiceAudioChunkFrames is how many 20 ms frames one write carries. One frame
// would be 50 writes a second for a 40 ms round trip's worth of latency saved;
// two keeps the syscall rate sane and is still well inside what a jitter buffer
// absorbs.
const voiceAudioChunkFrames = 2

// voiceBridge returns the local visor's host-app audio device, or writes the
// reason there isn't one and returns nil. The two failure modes are worth
// keeping apart: a remote visor is a bad request, while a local visor that
// opens its own device is a working visor this route simply does not apply to.
func (hv *Hypervisor) voiceBridge(w http.ResponseWriter, r *http.Request, ctx *httpCtx) *skycall.Bridge {
	if ctx.isRemote {
		httputil.WriteJSON(w, r, http.StatusBadRequest,
			map[string]string{"error": "voice audio is local to this visor"})
		return nil
	}
	if hv.visor == nil {
		httputil.WriteJSON(w, r, http.StatusServiceUnavailable,
			map[string]string{"error": ErrVoiceDisabled.Error()})
		return nil
	}
	bridge := hv.visor.VoiceAudioBridge()
	if bridge == nil {
		httputil.WriteJSON(w, r, http.StatusServiceUnavailable,
			map[string]string{"error": "this visor uses its own audio device"})
		return nil
	}
	return bridge
}

// postVoiceMic → POST /skychat/voice/mic : the request body is the host app's
// microphone, streamed as int16 PCM for as long as it wants to hold the call.
// Returns when the app closes the body or the stream goes quiet past the
// deadline.
func (hv *Hypervisor) postVoiceMic() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		bridge := hv.voiceBridge(w, r, ctx)
		if bridge == nil {
			return
		}
		defer func() { _ = r.Body.Close() }() //nolint:errcheck

		rc := http.NewResponseController(w)
		buf := make([]byte, skycall.FrameSamples*2*voiceAudioChunkFrames)
		pcm := make([]int16, len(buf)/2)
		// A read can split a sample down the middle. The odd byte is carried
		// to the FRONT of the next read rather than dropped: drop one byte and
		// every sample after it is assembled from the wrong pair — not an
		// error anywhere, just noise for the rest of the call.
		pending := 0

		for {
			if err := rc.SetReadDeadline(time.Now().Add(voiceAudioIOTimeout)); err != nil {
				// A transport without deadline control still works; it just
				// re-inherits the server's own (much shorter) ReadTimeout.
				hv.log(r).WithError(err).Debug("voice mic: could not extend read deadline")
			}
			n, err := r.Body.Read(buf[pending:])
			if total := pending + n; total > 0 {
				samples := total / 2
				for i := range pcm[:samples] {
					pcm[i] = int16(binary.LittleEndian.Uint16(buf[i*2:])) //nolint:gosec // LE bit reinterpretation
				}
				bridge.Push(pcm[:samples])
				pending = total % 2
				// The bounds are guaranteed (an odd total is at least 1, and
				// total never exceeds len(buf)) but stated so the reader — and
				// the analyser — can see it without reconstructing the loop.
				if last := total - 1; pending == 1 && last >= 0 && last < len(buf) {
					buf[0] = buf[last]
				}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					hv.log(r).WithError(err).Debug("voice mic: stream ended")
				}
				httputil.WriteJSON(w, r, http.StatusOK, map[string]bool{"ok": true})
				return
			}
			if r.Context().Err() != nil {
				return
			}
		}
	})
}

// getVoiceSpeaker → GET /skychat/voice/speaker : streams what the calls want
// played, as int16 PCM, paced at the frame rate, until the app disconnects.
//
// Silence is sent too, rather than nothing: playback on the other end wants a
// frame every tick or its own buffer underruns and clicks, and a stream that
// goes quiet is indistinguishable from one that died.
func (hv *Hypervisor) getVoiceSpeaker() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		bridge := hv.voiceBridge(w, r, ctx)
		if bridge == nil {
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		rc := http.NewResponseController(w)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Cache-Control", "no-store")
		// Defeat proxy buffering: held-back audio is worse than dropped audio.
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		pcm := make([]int16, skycall.FrameSamples*voiceAudioChunkFrames)
		buf := make([]byte, len(pcm)*2)
		tick := time.NewTicker(
			time.Duration(len(pcm)) * time.Second / time.Duration(skycall.SampleRate))
		defer tick.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-tick.C:
			}
			bridge.Pull(pcm)
			for i, s := range pcm {
				binary.LittleEndian.PutUint16(buf[i*2:], uint16(s)) //nolint:gosec // LE bit reinterpretation
			}
			if err := rc.SetWriteDeadline(time.Now().Add(voiceAudioIOTimeout)); err != nil {
				hv.log(r).WithError(err).Debug("voice speaker: could not extend write deadline")
			}
			if _, err := w.Write(buf); err != nil {
				return
			}
			flusher.Flush()
		}
	})
}
