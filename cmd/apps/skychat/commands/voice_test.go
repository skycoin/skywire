// Package commands cmd/apps/skychat/commands/voice_test.go
//
// Unit coverage for the browser-facing /voice HTTP proxy: each handler relays
// to the visor's Voice* RPC (a fake here) and shapes the response, 503s when the
// RPC is down, and validates input. Mirrors group_handlers_test.go.
package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// voiceAPI is a fake visor.API capturing the voice calls the handlers make.
type voiceAPI struct {
	visorAPIShim
	called   cipher.PubKey
	dialed   cipher.PubKey
	dialing  []visorapi.VoiceDialingInfo
	answered []string
	declined []string
	hungup   []string
	muted    []struct {
		id           string
		mic, speaker bool
	}
	active   []string
	incoming []string
	sent     []int16
	recv     []int16
	// ringback tones: the visor's own, and the one per outbound call.
	myTone   visorapi.VoiceRingback
	dialTone map[string]visorapi.VoiceRingback
}

func (a *voiceAPI) VoiceSetRingback(t visorapi.VoiceRingback) error {
	if len(t.Data) > 0 && !strings.HasPrefix(t.Mime, "audio/") {
		return errors.New("voice: \"" + t.Mime + "\" is not an audio format a ringback tone can be")
	}
	a.myTone = t
	return nil
}
func (a *voiceAPI) VoiceRingback() (visorapi.VoiceRingback, error) { return a.myTone, nil }
func (a *voiceAPI) VoiceDialRingback(id string) (visorapi.VoiceRingback, error) {
	if t, ok := a.dialTone[id]; ok {
		return t, nil
	}
	return visorapi.VoiceRingback{}, errors.New("voice: no ringback tone for that call")
}

func (a *voiceAPI) VoiceCall(peer cipher.PubKey) (string, error) {
	a.called = peer
	return "call-1", nil
}

// VoiceDial is what the handler uses: the blocking VoiceCall could never
// answer the browser, because a ring outlasts the server's write timeout.
func (a *voiceAPI) VoiceDial(peer cipher.PubKey) (string, error) {
	a.dialed = peer
	return "call-1", nil
}

func (a *voiceAPI) VoiceDialing() ([]visorapi.VoiceDialingInfo, error) { return a.dialing, nil }
func (a *voiceAPI) VoiceAnswer(id string) error                        { a.answered = append(a.answered, id); return nil }
func (a *voiceAPI) VoiceDecline(id string) error                       { a.declined = append(a.declined, id); return nil }
func (a *voiceAPI) VoiceHangup(id string) error                        { a.hungup = append(a.hungup, id); return nil }
func (a *voiceAPI) VoiceActive() ([]string, error)                     { return a.active, nil }
func (a *voiceAPI) VoiceIncoming() ([]string, error)                   { return a.incoming, nil }
func (a *voiceAPI) VoiceCallAudio(string) ([]int16, []int16, error) {
	return a.sent, a.recv, nil
}
func (a *voiceAPI) VoiceMute(id string, mic, speaker bool) error {
	a.muted = append(a.muted, struct {
		id           string
		mic, speaker bool
	}{id, mic, speaker})
	return nil
}

func TestVoiceCallHandler(t *testing.T) {
	fake := &voiceAPI{}
	withFakePairRPC(t, fake)
	pk, _ := cipher.GenerateKeyPair()

	// POST {peer} → {call_id}.
	rr := httptest.NewRecorder()
	voiceCallHandler()(rr, httptest.NewRequest(http.MethodPost, "/voice/call", strings.NewReader(`{"peer":"`+pk.Hex()+`"}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("call: code=%d body=%q", rr.Code, rr.Body.String())
	}
	var resp struct {
		CallID string `json:"call_id"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil || resp.CallID != "call-1" {
		t.Errorf("call resp=%q err=%v", rr.Body.String(), err)
	}
	if fake.dialed != pk {
		t.Errorf("VoiceDial got peer %s, want %s", fake.dialed.Hex(), pk.Hex())
	}
	if fake.called != (cipher.PubKey{}) {
		t.Errorf("the handler must not use the blocking VoiceCall: it cannot answer in time")
	}

	// Wrong method → 405.
	rr = httptest.NewRecorder()
	voiceCallHandler()(rr, httptest.NewRequest(http.MethodGet, "/voice/call", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: code=%d, want 405", rr.Code)
	}

	// Bad peer pk → 400.
	rr = httptest.NewRecorder()
	voiceCallHandler()(rr, httptest.NewRequest(http.MethodPost, "/voice/call", strings.NewReader(`{"peer":"nothex"}`)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("bad peer: code=%d, want 400", rr.Code)
	}
}

func TestVoiceActionHandlers(t *testing.T) {
	fake := &voiceAPI{}
	withFakePairRPC(t, fake)

	answer := voiceActionHandler(func(c visorapi.API, id string) error { return c.VoiceAnswer(id) })
	hangup := voiceActionHandler(func(c visorapi.API, id string) error { return c.VoiceHangup(id) })

	// Answer {call_id}.
	rr := httptest.NewRecorder()
	answer(rr, httptest.NewRequest(http.MethodPost, "/voice/answer", strings.NewReader(`{"call_id":"c9"}`)))
	if rr.Code != http.StatusOK || len(fake.answered) != 1 || fake.answered[0] != "c9" {
		t.Errorf("answer: code=%d answered=%v", rr.Code, fake.answered)
	}

	// Missing call_id → 400.
	rr = httptest.NewRecorder()
	hangup(rr, httptest.NewRequest(http.MethodPost, "/voice/hangup", strings.NewReader(`{}`)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("no call_id: code=%d, want 400", rr.Code)
	}

	// Wrong method → 405.
	rr = httptest.NewRecorder()
	hangup(rr, httptest.NewRequest(http.MethodGet, "/voice/hangup", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: code=%d, want 405", rr.Code)
	}
}

func TestVoiceMuteHandler(t *testing.T) {
	fake := &voiceAPI{}
	withFakePairRPC(t, fake)

	rr := httptest.NewRecorder()
	voiceMuteHandler()(rr, httptest.NewRequest(http.MethodPost, "/voice/mute", strings.NewReader(`{"call_id":"c1","mic":true,"speaker":false}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("mute: code=%d body=%q", rr.Code, rr.Body.String())
	}
	if len(fake.muted) != 1 || fake.muted[0].id != "c1" || !fake.muted[0].mic || fake.muted[0].speaker {
		t.Errorf("mute captured = %+v", fake.muted)
	}
}

func TestVoiceListHandlers(t *testing.T) {
	fake := &voiceAPI{
		active:   []string{"c1", "c2"},
		incoming: []string{"c3 from abcdef"},
	}
	withFakePairRPC(t, fake)

	active := voiceListHandler("VoiceActive", func(c visorapi.API) ([]string, error) { return c.VoiceActive() })
	incoming := voiceListHandler("VoiceIncoming", func(c visorapi.API) ([]string, error) { return c.VoiceIncoming() })

	rr := httptest.NewRecorder()
	active(rr, httptest.NewRequest(http.MethodGet, "/voice/active", nil))
	var got []string
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil || len(got) != 2 {
		t.Errorf("active: body=%q err=%v", rr.Body.String(), err)
	}

	rr = httptest.NewRecorder()
	incoming(rr, httptest.NewRequest(http.MethodGet, "/voice/incoming", nil))
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil || len(got) != 1 || !strings.Contains(got[0], "from") {
		t.Errorf("incoming: body=%q err=%v", rr.Body.String(), err)
	}

	// Wrong method → 405.
	rr = httptest.NewRecorder()
	active(rr, httptest.NewRequest(http.MethodPost, "/voice/active", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST list: code=%d, want 405", rr.Code)
	}
}

// An outbound call that is still ringing exists in no active list and no
// ringing list — its id lives only in the caller's dialing set. Hanging up
// takes an id, so this route is the difference between a call that can be
// called off and one that cannot.
func TestVoiceDialingHandler(t *testing.T) {
	fake := &voiceAPI{
		dialing: []visorapi.VoiceDialingInfo{{CallID: "c9", Peer: "abcdef"}},
	}
	withFakePairRPC(t, fake)

	rr := httptest.NewRecorder()
	voiceDialingHandler()(rr, httptest.NewRequest(http.MethodGet, "/voice/dialing", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("dialing: code=%d body=%q", rr.Code, rr.Body.String())
	}
	var got []visorapi.VoiceDialingInfo
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("dialing: body=%q err=%v", rr.Body.String(), err)
	}
	if len(got) != 1 || got[0].CallID != "c9" || got[0].Peer != "abcdef" {
		t.Errorf("dialing = %+v, want one call c9 with its peer", got)
	}

	// Empty must be [] and not null: the UI reads .length off it.
	fake.dialing = nil
	rr = httptest.NewRecorder()
	voiceDialingHandler()(rr, httptest.NewRequest(http.MethodGet, "/voice/dialing", nil))
	if body := strings.TrimSpace(rr.Body.String()); body != "[]" {
		t.Errorf("empty dialing = %q, want []", body)
	}
}

func TestVoiceLevelsHandler(t *testing.T) {
	// Half-scale sent, silence recv → sent RMS ~0.5, recv 0.
	sent := make([]int16, 4800)
	for i := range sent {
		sent[i] = 16384
	}
	fake := &voiceAPI{sent: sent, recv: nil}
	withFakePairRPC(t, fake)

	rr := httptest.NewRecorder()
	voiceLevelsHandler()(rr, httptest.NewRequest(http.MethodGet, "/voice/levels?call=c1", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("levels: code=%d body=%q", rr.Code, rr.Body.String())
	}
	var lv struct {
		Sent float64 `json:"sent"`
		Recv float64 `json:"recv"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &lv); err != nil {
		t.Fatal(err)
	}
	if lv.Sent < 0.4 || lv.Sent > 0.6 || lv.Recv != 0 {
		t.Errorf("levels sent=%.3f recv=%.3f, want sent~0.5 recv=0", lv.Sent, lv.Recv)
	}

	// Missing call param → 400.
	rr = httptest.NewRecorder()
	voiceLevelsHandler()(rr, httptest.NewRequest(http.MethodGet, "/voice/levels", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("no call: code=%d, want 400", rr.Code)
	}
}

func TestVoiceAudioHandler(t *testing.T) {
	fake := &voiceAPI{sent: []int16{1, 2, 3}, recv: []int16{-4, -5}}
	withFakePairRPC(t, fake)

	rr := httptest.NewRecorder()
	voiceAudioHandler()(rr, httptest.NewRequest(http.MethodGet, "/voice/audio?call=c1", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("audio: code=%d body=%q", rr.Code, rr.Body.String())
	}
	var pcm struct {
		Sent []int16 `json:"sent"`
		Recv []int16 `json:"recv"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &pcm); err != nil {
		t.Fatal(err)
	}
	if len(pcm.Sent) != 3 || len(pcm.Recv) != 2 || pcm.Sent[0] != 1 || pcm.Recv[0] != -4 {
		t.Errorf("pcm = %+v", pcm)
	}

	// Missing call param → 400.
	rr = httptest.NewRecorder()
	voiceAudioHandler()(rr, httptest.NewRequest(http.MethodGet, "/voice/audio", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("no call: code=%d, want 400", rr.Code)
	}
}

func TestVoiceHandlers_503WhenRPCDown(t *testing.T) {
	pairRPCMu.Lock()
	prev := pairRPC
	pairRPC = nil
	pairRPCMu.Unlock()
	t.Cleanup(func() {
		pairRPCMu.Lock()
		pairRPC = prev
		pairRPCMu.Unlock()
	})
	for _, tc := range []struct {
		name string
		h    http.HandlerFunc
		req  *http.Request
	}{
		{"call", voiceCallHandler(), httptest.NewRequest(http.MethodPost, "/voice/call", strings.NewReader(`{}`))},
		{"answer", voiceActionHandler(func(c visorapi.API, id string) error { return c.VoiceAnswer(id) }), httptest.NewRequest(http.MethodPost, "/voice/answer", strings.NewReader(`{}`))},
		{"mute", voiceMuteHandler(), httptest.NewRequest(http.MethodPost, "/voice/mute", strings.NewReader(`{}`))},
		{"active", voiceListHandler("VoiceActive", func(c visorapi.API) ([]string, error) { return c.VoiceActive() }), httptest.NewRequest(http.MethodGet, "/voice/active", nil)},
		{"levels", voiceLevelsHandler(), httptest.NewRequest(http.MethodGet, "/voice/levels?call=x", nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			tc.h(rr, tc.req)
			if rr.Code != http.StatusServiceUnavailable {
				t.Errorf("code=%d, want 503", rr.Code)
			}
		})
	}
}

// TestVoiceMyRingbackHandler: the tone this visor plays to its callers can be
// set, previewed and cleared — and only as audio.
func TestVoiceMyRingbackHandler(t *testing.T) {
	fake := &voiceAPI{}
	withFakePairRPC(t, fake)
	h := voiceMyRingbackHandler()

	// None set → 404, so the settings screen can say "ordinary ring".
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/voice/my-ringback", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("get with none set: code=%d, want 404", rr.Code)
	}

	// PUT the audio with its type.
	tone := []byte("ID3 not really an mp3")
	req := httptest.NewRequest(http.MethodPut, "/voice/my-ringback", bytes.NewReader(tone))
	req.Header.Set("Content-Type", "audio/mpeg")
	rr = httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK || !bytes.Equal(fake.myTone.Data, tone) || fake.myTone.Mime != "audio/mpeg" {
		t.Fatalf("put: code=%d body=%q stored=%q %q", rr.Code, rr.Body.String(), fake.myTone.Data, fake.myTone.Mime)
	}

	// ?info=1 says so without sending the audio.
	rr = httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/voice/my-ringback?info=1", nil))
	var info struct {
		Set  bool   `json:"set"`
		Size int    `json:"size"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &info); err != nil || !info.Set || info.Size != len(tone) || info.Type != "audio/mpeg" {
		t.Fatalf("info: %q err=%v", rr.Body.String(), err)
	}

	// GET plays it back, typed and never sniffed.
	rr = httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/voice/my-ringback", nil))
	if rr.Code != http.StatusOK || !bytes.Equal(rr.Body.Bytes(), tone) {
		t.Fatalf("get: code=%d body=%q", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "audio/mpeg" {
		t.Errorf("Content-Type = %q", ct)
	}
	if rr.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("the tone is served sniffable")
	}

	// A type the visor refuses is the user's mistake, not a gateway error.
	req = httptest.NewRequest(http.MethodPut, "/voice/my-ringback", strings.NewReader("<script>"))
	req.Header.Set("Content-Type", "text/html")
	rr = httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("put text/html: code=%d, want 400", rr.Code)
	}

	// Over the limit is refused before it reaches the visor.
	req = httptest.NewRequest(http.MethodPut, "/voice/my-ringback", bytes.NewReader(make([]byte, maxRingbackUpload+1)))
	req.Header.Set("Content-Type", "audio/mpeg")
	rr = httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized put: code=%d, want 413", rr.Code)
	}

	// DELETE clears it.
	rr = httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodDelete, "/voice/my-ringback", nil))
	if rr.Code != http.StatusOK || len(fake.myTone.Data) != 0 {
		t.Fatalf("delete: code=%d still stored=%q", rr.Code, fake.myTone.Data)
	}
}

// TestVoiceDialRingbackHandler: the peer's tone for an outbound call is served
// once it is here, 404 until then — and a peer-chosen type that is not audio
// is never what the page is served as.
func TestVoiceDialRingbackHandler(t *testing.T) {
	fake := &voiceAPI{dialTone: map[string]visorapi.VoiceRingback{
		"c1":   {Data: []byte("tone"), Mime: "audio/ogg"},
		"evil": {Data: []byte("<script>alert(1)</script>"), Mime: "text/html"},
	}}
	withFakePairRPC(t, fake)
	h := voiceDialRingbackHandler()

	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/voice/ringback?call=c1", nil))
	if rr.Code != http.StatusOK || rr.Body.String() != "tone" || rr.Header().Get("Content-Type") != "audio/ogg" {
		t.Fatalf("c1: code=%d type=%q body=%q", rr.Code, rr.Header().Get("Content-Type"), rr.Body.String())
	}

	rr = httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/voice/ringback?call=nope", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("unknown call: code=%d, want 404", rr.Code)
	}

	rr = httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/voice/ringback?call=evil", nil))
	if ct := rr.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("a non-audio tone was served as %q", ct)
	}
	if rr.Header().Get("Content-Security-Policy") != "sandbox" {
		t.Error("the tone is not sandboxed")
	}

	rr = httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/voice/ringback", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("no call: code=%d, want 400", rr.Code)
	}
}
