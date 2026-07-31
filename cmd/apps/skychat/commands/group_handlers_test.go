// Package commands cmd/apps/skychat/commands/group_handlers_test.go
//
// Unit coverage for the browser-facing /group HTTP proxy: each handler
// relays to the visor's GroupX RPC (a fake here) and shapes the response.
package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor"
)

// groupAPI is a fake visor.API capturing the group calls the handlers make.
type groupAPI struct {
	visorAPIShim
	groups   []visor.GroupInfo
	created  visor.GroupCreateArgs
	joined   visor.GroupJoinArgs
	sent     []visor.GroupSendArgs
	unsent   []visor.GroupUnsendArgs
	deleted  []string
	left     []string
	promoted []cipher.PubKey
	demoted  []cipher.PubKey
	history  []visor.GroupMessage // returned by GroupHistory when non-nil
	// fileKey, when set, is handed back by GroupFileKey as both the seal
	// and open key — the encrypted-group case. Empty means a plaintext
	// group.
	fileKey      []byte
	fileKeyCalls []visor.GroupFileKeyArgs
}

func (a *groupAPI) GroupList() ([]visor.GroupInfo, error) { return a.groups, nil }
func (a *groupAPI) GroupCreate(args visor.GroupCreateArgs) (visor.GroupInfo, string, error) {
	a.created = args
	return visor.GroupInfo{ID: "gid-1", Name: args.Name, Mode: args.Mode}, "skychat:invite:xyz", nil
}
func (a *groupAPI) GroupJoin(args visor.GroupJoinArgs) (visor.GroupInfo, error) {
	a.joined = args
	return visor.GroupInfo{ID: "gid-2", Name: "joined"}, nil
}
func (a *groupAPI) GroupGet(id string) (visor.GroupInfo, error) {
	return visor.GroupInfo{ID: id, Name: "g"}, nil
}
func (a *groupAPI) GroupInvite(string) (string, error) { return "skychat:invite:reemit", nil } //nolint
func (a *groupAPI) GroupSend(args visor.GroupSendArgs) error {
	a.sent = append(a.sent, args)
	return nil
}

// GroupFileKey answers the attachment-key lookup. Zero value = a plaintext
// group (no key, nothing to seal), which is what most of these tests want;
// set fileKey to exercise the sealed path.
func (a *groupAPI) GroupFileKey(args visor.GroupFileKeyArgs) (visor.GroupFileKeyResult, error) {
	a.fileKeyCalls = append(a.fileKeyCalls, args)
	if len(a.fileKey) == 0 {
		return visor.GroupFileKeyResult{}, nil
	}
	return visor.GroupFileKeyResult{
		Seal:      a.fileKey,
		Open:      [][]byte{a.fileKey},
		Encrypted: true,
	}, nil
}
func (a *groupAPI) GroupUnsend(args visor.GroupUnsendArgs) error {
	a.unsent = append(a.unsent, args)
	return nil
}
func (a *groupAPI) GroupLeave(id string) error { a.left = append(a.left, id); return nil }
func (a *groupAPI) GroupDelete(id string) error {
	a.deleted = append(a.deleted, id)
	return nil
}
func (a *groupAPI) GroupPromoteAdmin(id string, pk cipher.PubKey) (visor.GroupInfo, error) {
	a.promoted = append(a.promoted, pk)
	return visor.GroupInfo{ID: id, Admins: a.promoted}, nil
}
func (a *groupAPI) GroupDemoteAdmin(id string, pk cipher.PubKey) (visor.GroupInfo, error) {
	a.demoted = append(a.demoted, pk)
	return visor.GroupInfo{ID: id}, nil
}
func (a *groupAPI) GroupHistory(id string, _ int) ([]visor.GroupMessage, error) {
	if a.history != nil {
		return a.history, nil
	}
	return []visor.GroupMessage{{GroupID: id, Text: "old", TS: time.Now().UTC()}}, nil
}

func TestGroupRootHandler_ListAndCreate(t *testing.T) {
	fake := &groupAPI{groups: []visor.GroupInfo{{ID: "a", Name: "one"}, {ID: "b", Name: "two"}}}
	withFakePairRPC(t, fake)

	// GET → list
	rr := httptest.NewRecorder()
	groupRootHandler()(rr, httptest.NewRequest(http.MethodGet, "/group", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /group: code=%d body=%q", rr.Code, rr.Body.String())
	}
	var list []visor.GroupInfo
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil || len(list) != 2 {
		t.Fatalf("list decode err=%v len=%d", err, len(list))
	}

	// POST → create (default mode public)
	rr = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/group", strings.NewReader(`{"name":"room","mode":"public"}`))
	groupRootHandler()(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /group: code=%d body=%q", rr.Code, rr.Body.String())
	}
	var created struct {
		Info   visor.GroupInfo `json:"info"`
		Invite string          `json:"invite"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if fake.created.Name != "room" || created.Invite == "" || created.Info.ID != "gid-1" {
		t.Errorf("create: captured=%+v resp=%+v", fake.created, created)
	}
}

func TestGroupRootHandler_CreateRejectsBadInput(t *testing.T) {
	withFakePairRPC(t, &groupAPI{})
	cases := map[string]string{
		"missing name": `{"mode":"public"}`,
		"bad mode":     `{"name":"x","mode":"bogus"}`,
	}
	for name, bodyStr := range cases {
		t.Run(name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			groupRootHandler()(rr, httptest.NewRequest(http.MethodPost, "/group", strings.NewReader(bodyStr)))
			if rr.Code != http.StatusBadRequest {
				t.Errorf("code=%d, want 400", rr.Code)
			}
		})
	}
}

func TestGroupJoinHandler(t *testing.T) {
	fake := &groupAPI{}
	withFakePairRPC(t, fake)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/group/join", strings.NewReader(`{"invite":"skychat:invite:abc"}`))
	groupJoinHandler()(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d body=%q", rr.Code, rr.Body.String())
	}
	if fake.joined.Invite != "skychat:invite:abc" {
		t.Errorf("join invite = %q", fake.joined.Invite)
	}
	// Empty invite → 400.
	rr = httptest.NewRecorder()
	groupJoinHandler()(rr, httptest.NewRequest(http.MethodPost, "/group/join", strings.NewReader(`{"invite":"  "}`)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("empty invite: code=%d, want 400", rr.Code)
	}
}

func TestGroupItemHandler(t *testing.T) {
	fake := &groupAPI{}
	withFakePairRPC(t, fake)
	h := groupItemHandler()

	// GET info
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/group/gid-9", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET info: code=%d", rr.Code)
	}

	// POST send
	rr = httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/group/gid-9/send", strings.NewReader(`{"text":"hi group"}`)))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("send: code=%d body=%q", rr.Code, rr.Body.String())
	}
	if len(fake.sent) != 1 || fake.sent[0].ID != "gid-9" || fake.sent[0].Text != "hi group" {
		t.Errorf("send captured = %+v", fake.sent)
	}

	// GET invite
	rr = httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/group/gid-9/invite", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "skychat:invite") {
		t.Errorf("invite: code=%d body=%q", rr.Code, rr.Body.String())
	}

	// GET history
	rr = httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/group/gid-9/history?limit=10", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("history: code=%d", rr.Code)
	}

	// POST leave
	rr = httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/group/gid-9/leave", nil))
	if rr.Code != http.StatusNoContent || len(fake.left) != 1 {
		t.Errorf("leave: code=%d left=%v", rr.Code, fake.left)
	}

	// DELETE group
	rr = httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodDelete, "/group/gid-9", nil))
	if rr.Code != http.StatusNoContent || len(fake.deleted) != 1 {
		t.Errorf("delete: code=%d deleted=%v", rr.Code, fake.deleted)
	}
}

// Promote / demote had no route here for a long time: the visor RPC and
// the CLI both had them, so nothing looked missing, but a browser-only
// operator could never create a second admin — and an invite names the
// group's admins, so the founder stayed the only way in. Route coverage
// is what would have caught it.
func TestGroupItemHandler_PromoteDemote(t *testing.T) {
	fake := &groupAPI{}
	withFakePairRPC(t, fake)
	h := groupItemHandler()

	pk, _ := cipher.GenerateKeyPair()
	body := `{"pk":"` + pk.Hex() + `"}`

	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/group/gid-9/members/promote", strings.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("promote: code=%d body=%q", rr.Code, rr.Body.String())
	}
	if len(fake.promoted) != 1 || fake.promoted[0] != pk {
		t.Errorf("promote captured = %v, want [%s]", fake.promoted, pk)
	}

	rr = httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/group/gid-9/members/demote", strings.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("demote: code=%d body=%q", rr.Code, rr.Body.String())
	}
	if len(fake.demoted) != 1 || fake.demoted[0] != pk {
		t.Errorf("demote captured = %v, want [%s]", fake.demoted, pk)
	}
}

func TestGroupHandlers_503WhenRPCDown(t *testing.T) {
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
		{"root", groupRootHandler(), httptest.NewRequest(http.MethodGet, "/group", nil)},
		{"join", groupJoinHandler(), httptest.NewRequest(http.MethodPost, "/group/join", strings.NewReader(`{}`))},
		{"item", groupItemHandler(), httptest.NewRequest(http.MethodGet, "/group/x", nil)},
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
