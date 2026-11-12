package visor

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

type ui2VoiceAPI struct {
	ui2FakeAPI
	off      bool
	active   []string
	incoming []string
	dialed   cipher.PubKey
	muted    [2]bool
	answered string
}

func (a *ui2VoiceAPI) VoiceActive() ([]string, error) {
	if a.off {
		return nil, ErrVoiceDisabled
	}
	return a.active, nil
}

func (a *ui2VoiceAPI) VoiceIncoming() ([]string, error) { return a.incoming, nil }

func (a *ui2VoiceAPI) VoiceDialing() ([]visorapi.VoiceDialingInfo, error) {
	if a.dialed.Null() {
		return nil, nil
	}
	return []visorapi.VoiceDialingInfo{{CallID: "d1", Peer: a.dialed.Hex(), State: "ringing"}}, nil
}

func (a *ui2VoiceAPI) VoiceDial(pk cipher.PubKey) (string, error) { a.dialed = pk; return "d1", nil }

func (a *ui2VoiceAPI) VoiceAnswer(id string) error {
	a.answered, a.incoming, a.active = id, nil, []string{id}
	return nil
}

func (a *ui2VoiceAPI) VoiceMute(_ string, mic, spk bool) error {
	a.muted = [2]bool{mic, spk}
	return nil
}

func TestUI2Voice(t *testing.T) {
	peer, _ := cipher.GenerateKeyPair()
	api := &ui2VoiceAPI{incoming: []string{"c1 from " + peer.Hex()}}
	base := newUI2FakeServer(t, api)
	form := "application/x-www-form-urlencoded"
	post := func(op string, v url.Values) string {
		code, body := ui2HX(t, http.MethodPost, base+"/chat/voice/"+op, form, v.Encode())
		require.Equal(t, http.StatusOK, code, body)
		return body
	}

	_, body := ui2HX(t, http.MethodGet, base+"/chat/voice?peer="+peer.Hex(), "", "")
	require.Contains(t, body, "Incoming voice call from")
	require.Contains(t, body, peer.Hex())

	body = post("call", url.Values{"peer": {peer.Hex()}})
	require.Equal(t, peer, api.dialed)
	require.Contains(t, body, "ringing")

	body = post("answer", url.Values{"call_id": {"c1"}})
	require.Equal(t, "c1", api.answered)
	require.Contains(t, body, "data-voice-levels")
	require.Contains(t, body, "skychat/voice/levels?call=c1")

	post("mute", url.Values{"call_id": {"c1"}, "call": {"c1"}, "since": {"1"}, "mic": {"1"}})
	require.Equal(t, [2]bool{true, false}, api.muted)

	api.off = true
	_, body = ui2HX(t, http.MethodGet, base+"/chat/voice", "", "")
	require.Contains(t, body, `id="chat-voice" hidden`)
}
