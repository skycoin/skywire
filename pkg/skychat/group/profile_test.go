// Package group pkg/skychat/group/profile_test.go c4-app-chat
//
// The responder's disclosure policy, exercised over a net.Pipe rather than a
// real transport: what a stranger gets back for asking "who are you?" is
// decided entirely by handleProfileRequest, and none of it depends on dmsg
// being up.
package group

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skychat/message"
	"github.com/skycoin/skywire/pkg/skychat/profile"
)

// stubProfile is a ProfileProvider backed by a fixed value.
type stubProfile struct {
	p   profile.Profile
	err error
}

func (s stubProfile) Load() (profile.Profile, error) { return s.p, s.err }

func newProfileTestManager(myPK cipher.PubKey, src ProfileProvider) *Manager {
	return &Manager{
		myPK:       myPK,
		log:        logging.MustGetLogger("group.profile-test"),
		sessions:   make(map[string]*Session),
		profileSrc: src,
	}
}

func testPK(t *testing.T) cipher.PubKey {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	return pk
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(0, 0, color.RGBA{R: 0xff, A: 0xff})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// askProfile runs one request/response over a pipe and returns what the
// responder wrote.
func askProfile(t *testing.T, m *Manager) ProfileResponseMsg {
	t.Helper()
	srv, cli := net.Pipe()
	t.Cleanup(func() { _ = srv.Close(); _ = cli.Close() }) //nolint:errcheck

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.handleProfileRequest(srv, testPK(t))
	}()

	if err := cli.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	frame, err := message.ReadFrame(cli)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	<-done

	var resp ProfileResponseMsg
	if err := json.Unmarshal(frame, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func TestProfileResponseCarriesWhatWasSet(t *testing.T) {
	me := testPK(t)
	avatar := tinyPNG(t)
	m := newProfileTestManager(me, stubProfile{p: profile.Profile{
		Name: "Alice", Avatar: avatar, AvatarMime: profile.MimePNG,
	}})

	resp := askProfile(t, m)
	if resp.Kind != frameKindProfileResponse {
		t.Fatalf("kind = %q", resp.Kind)
	}
	if resp.PK != me {
		t.Fatalf("pk = %s, want this visor's own", resp.PK)
	}
	if resp.Name != "Alice" {
		t.Fatalf("name = %q", resp.Name)
	}
	if !bytes.Equal(resp.Avatar, avatar) || resp.AvatarMime != profile.MimePNG {
		t.Fatalf("avatar not round tripped (%d bytes, mime %q)", len(resp.Avatar), resp.AvatarMime)
	}
}

// "Nothing said" is a legitimate answer, and it must be an ANSWER: silence
// is how an unreachable host looks, and an asker should not be left retrying
// something that will not improve.
func TestProfileResponseIsEmptyWhenNothingIsPublished(t *testing.T) {
	me := testPK(t)
	for name, m := range map[string]*Manager{
		"no provider":  newProfileTestManager(me, nil),
		"empty value":  newProfileTestManager(me, stubProfile{}),
		"read failure": newProfileTestManager(me, stubProfile{err: errTestRead}),
	} {
		t.Run(name, func(t *testing.T) {
			resp := askProfile(t, m)
			if resp.Kind != frameKindProfileResponse {
				t.Fatalf("kind = %q — the asker got no usable answer", resp.Kind)
			}
			if resp.Name != "" || len(resp.Avatar) != 0 {
				t.Fatalf("answered %+v, want empty", resp)
			}
			if resp.PK != me {
				t.Fatalf("pk = %s", resp.PK)
			}
		})
	}
}

var errTestRead = &testErr{"store unavailable"}

type testErr struct{ s string }

func (e *testErr) Error() string { return e.s }

// A visor asking about itself is answered from the store, with no dial —
// the same courtesy ProbeGroup and Catalog give.
func TestFetchProfileAnswersLocallyForOwnKey(t *testing.T) {
	me := testPK(t)
	m := newProfileTestManager(me, stubProfile{p: profile.Profile{Name: "Me"}})

	// No dmsg client is set, so a fetch that tried to dial would fail.
	for _, ask := range []cipher.PubKey{me, {}} {
		got, err := m.FetchProfile(t.Context(), ask)
		if err != nil {
			t.Fatalf("FetchProfile(%v): %v", ask, err)
		}
		if got.Name != "Me" {
			t.Fatalf("FetchProfile(%v) = %+v", ask, got)
		}
	}
}

func TestLocalProfileWithoutProviderIsEmpty(t *testing.T) {
	m := newProfileTestManager(testPK(t), nil)
	p, err := m.LocalProfile()
	if err != nil {
		t.Fatalf("LocalProfile: %v", err)
	}
	if !p.IsZero() {
		t.Fatalf("got %+v, want empty", p)
	}
}

func TestSetProfileProviderTakesEffect(t *testing.T) {
	m := newProfileTestManager(testPK(t), nil)
	m.SetProfileProvider(stubProfile{p: profile.Profile{Name: "Later"}})
	p, err := m.LocalProfile()
	if err != nil {
		t.Fatalf("LocalProfile: %v", err)
	}
	if p.Name != "Later" {
		t.Fatalf("provider not installed: %+v", p)
	}
}

// The discriminator is what keeps the three questions on this port apart. A
// profile frame must not look like a catalog or a join request.
func TestProfileRequestFrameIsDistinct(t *testing.T) {
	body, err := json.Marshal(&ProfileRequestMsg{Kind: frameKindProfileRequest, TS: time.Now().UTC()})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var sniff relayFrameProbe
	if err := json.Unmarshal(body, &sniff); err != nil {
		t.Fatalf("sniff: %v", err)
	}
	if sniff.Kind != frameKindProfileRequest {
		t.Fatalf("discriminator = %q", sniff.Kind)
	}
	if sniff.Kind == frameKindCatalogRequest || sniff.Kind == frameKindJoinRequest {
		t.Fatal("profile frames collide with another kind on this port")
	}
}
