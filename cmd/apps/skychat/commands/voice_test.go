// Package commands cmd/apps/skychat/commands/voice_test.go
//
// Unit coverage for the browser-facing /voice HTTP proxy: each handler relays
// to the visor's Voice* RPC (a fake here) and shapes the response, 503s when the
// RPC is down, and validates input. Mirrors group_handlers_test.go.
package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor"
)

// voiceAPI is a fake visor.API capturing the voice calls the handlers make.
type voiceAPI struct {
	visorAPIShim
	called   cipher.PubKey
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
}

func (a *voiceAPI) VoiceCall(peer cipher.PubKey) (string, error) { a.called = peer; return "call-1", nil }
func (a *voiceAPI) VoiceAnswer(id string) error                  { a.answered = append(a.answered, id); return nil }
func (a *voiceAPI) VoiceDecline(id string) error                 { a.declined = append(a.declined, id); return nil }
func (a *voiceAPI) VoiceHangup(id string) error                  { a.hungup = append(a.hungup, id); return nil }
func (a *voiceAPI) VoiceActive() ([]string, error)               { return a.active, nil }
func (a *voiceAPI) VoiceIncoming() ([]string, error)             { return a.incoming, nil }
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
	if fake.called != pk {
		t.Errorf("VoiceCall got peer %s, want %s", fake.called.Hex(), pk.Hex())
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

	answer := voiceActionHandler(func(c visor.API, id string) error { return c.VoiceAnswer(id) })
	hangup := voiceActionHandler(func(c visor.API, id string) error { return c.VoiceHangup(id) })

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

	active := voiceListHandler("VoiceActive", func(c visor.API) ([]string, error) { return c.VoiceActive() })
	incoming := voiceListHandler("VoiceIncoming", func(c visor.API) ([]string, error) { return c.VoiceIncoming() })

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
		{"answer", voiceActionHandler(func(c visor.API, id string) error { return c.VoiceAnswer(id) }), httptest.NewRequest(http.MethodPost, "/voice/answer", strings.NewReader(`{}`))},
		{"mute", voiceMuteHandler(), httptest.NewRequest(http.MethodPost, "/voice/mute", strings.NewReader(`{}`))},
		{"active", voiceListHandler("VoiceActive", func(c visor.API) ([]string, error) { return c.VoiceActive() }), httptest.NewRequest(http.MethodGet, "/voice/active", nil)},
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
