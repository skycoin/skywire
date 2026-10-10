package autoconfigui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"

	"github.com/skycoin/skywire/pkg/skywireconfig/autoconfigcmd"
)

func testFlags() []autoconfigcmd.Flag {
	return []autoconfigcmd.Flag{
		{Name: "ishv", Type: "bool", EnvKey: "ISHYPERVISOR", EnvFormat: "bool"},
		{Name: "no-ishv", Type: "bool", EnvKey: "ISHYPERVISOR", EnvFormat: "bool", EnvNegate: true},
		{Name: "pty-rpc-exec", Type: "bool", EnvKey: "PTYRPCEXEC", EnvFormat: "bool"},
		{Name: "min-hops", Type: "int", EnvKey: "MINHOPS", EnvFormat: "int", EnvDefault: "0"},
		{Name: "rewardaddr", Type: "string", EnvKey: "REWARDSKYADDR", EnvFormat: "string"},
		{Name: "hvpks", Type: "string", EnvKey: "HYPERVISORPKS", EnvFormat: "bashArray"},
		{Name: "sk", Type: "string", EnvKey: "SK", EnvFormat: "string"},
		{Name: "verbose", Type: "bool"},
	}
}

func writeEnv(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "skywire.conf")
	if err := os.WriteFile(p, []byte(sampleEnv), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const sampleEnv = `ISHYPERVISOR=true
MINHOPS=2
REWARDSKYADDR='abc'
HYPERVISORPKS=('pk1' 'pk2')
SK='secret'
`

func field(m *Model, name string) *Field {
	for _, f := range m.Fields {
		if f.Name == name {
			return f
		}
	}
	return nil
}

func TestModelCurrentValues(t *testing.T) {
	m := NewModel(testFlags(), writeEnv(t))
	if field(m, "no-ishv") != nil || field(m, "verbose") != nil {
		t.Fatal("negate twin and run-only flags must not be fields")
	}
	for name, want := range map[string]string{
		"ishv": "true", "min-hops": "2", "rewardaddr": "abc", "hvpks": "pk1,pk2", "pty-rpc-exec": "false", "sk": "",
	} {
		if got := field(m, name).Current; got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if !field(m, "sk").Secret || field(m, "pty-rpc-exec").Set {
		t.Error("secret or set flag wrong")
	}
	if args, err := m.Args(); err != nil || len(args) != 0 {
		t.Fatalf("untouched model emits %v %v", args, err)
	}
}

func TestModelMissingFileUsesDefaults(t *testing.T) {
	m := NewModel(testFlags(), filepath.Join(t.TempDir(), "none"))
	if field(m, "min-hops").Current != "0" {
		t.Fatal("default not shown")
	}
}

func TestArgsOnlyChanged(t *testing.T) {
	m := NewModel(testFlags(), writeEnv(t))
	mustSet(t, m, "ishv", "false")
	mustSet(t, m, "pty-rpc-exec", "true")
	mustSet(t, m, "min-hops", " 3 ")
	mustSet(t, m, "hvpks", "a,b")
	mustSet(t, m, "sk", "new")
	m.NoRestart = true
	got, err := m.Args()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--no-ishv", "--pty-rpc-exec=true", "--min-hops=3", "--hvpks=a,b", "--sk=new", "--no-restart"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v want %v", got, want)
	}
	mustSet(t, m, "ishv", "true")
	got = mustArgs(t, m)
	if strings.Contains(strings.Join(got, " "), "ishv") {
		t.Fatalf("reverted field still emitted: %v", got)
	}
}

func TestArgsValidation(t *testing.T) {
	m := NewModel(testFlags(), writeEnv(t))
	mustSet(t, m, "min-hops", "many")
	if _, err := m.Args(); err == nil {
		t.Fatal("bad int accepted")
	}
	m = NewModel(testFlags(), writeEnv(t))
	mustSet(t, m, "pty-rpc-exec", "maybe")
	if _, err := m.Args(); err == nil {
		t.Fatal("bad bool accepted")
	}
	if err := m.Set("nope", "1"); err == nil {
		t.Fatal("unknown flag accepted")
	}
}

func TestCommandQuoting(t *testing.T) {
	m := NewModel(testFlags(), writeEnv(t))
	mustSet(t, m, "rewardaddr", "two words")
	mustSet(t, m, "hvpks", "a,b")
	cmd, err := m.Command()
	if err != nil {
		t.Fatal(err)
	}
	want := "skywire autoconfig --rewardaddr='two words' --hvpks=a,b"
	if cmd != want {
		t.Fatalf("got %q want %q", cmd, want)
	}
	mustSet(t, m, "rewardaddr", "it's")
	cmd = mustCommand(t, m)
	if !strings.Contains(cmd, `--rewardaddr='it'\''s'`) {
		t.Fatalf("bad quote: %s", cmd)
	}
}

func TestRoundTripThroughFile(t *testing.T) {
	// Real flag set: every field, unedited, must yield no args.
	p := writeEnv(t)
	m := NewModel(autoconfigcmd.Describe(), p)
	if len(m.Fields) < 20 {
		t.Fatalf("only %d fields", len(m.Fields))
	}
	if args, err := m.Args(); err != nil || len(args) != 0 {
		t.Fatalf("unedited real model emits %v %v", args, err)
	}
	for _, f := range m.Fields {
		if f.Group == "Other" {
			t.Logf("ungrouped: %s", f.Name)
		}
	}
}

func TestTUIState(t *testing.T) {
	m := NewModel(testFlags(), writeEnv(t))
	s := newTUIState(m)
	key := func(k tcell.Key, r string) { s.handleKey(tcell.NewEventKey(k, r, 0)) }
	if s.field().Name != "ishv" {
		t.Fatalf("start on %s", s.field().Name)
	}
	key(tcell.KeyEnter, "") // toggles the bool
	if s.field().Value != "false" {
		t.Fatal("bool not toggled")
	}
	key(tcell.KeyRune, "r")
	if s.field().Changed() {
		t.Fatal("revert failed")
	}
	for s.field().Name != "min-hops" {
		key(tcell.KeyDown, "")
	}
	key(tcell.KeyEnter, "")
	key(tcell.KeyBackspace2, "")
	key(tcell.KeyRune, "5")
	key(tcell.KeyEnter, "")
	if s.field().Value != "5" {
		t.Fatalf("edit gave %q", s.field().Value)
	}
	key(tcell.KeyRune, "p")
	if !s.done || s.action != ActionPrint {
		t.Fatal("print not chosen")
	}
}

func TestGroups(t *testing.T) {
	for name, want := range map[string]string{
		"ishv": "Hypervisor", "no-ishv": "Hypervisor", "dmsg-server-public": "Dmsg server", "deployment-redis": "Deployment",
		"vpnrouter-ssid": "VPN router", "skycoinwebaddr": "Skycoin", "zzz": "Other",
	} {
		if got := GroupOf(name); got != want {
			t.Errorf("%s: %s want %s", name, got, want)
		}
	}
}

func webFixture(t *testing.T) (*Web, *[][]string) {
	t.Helper()
	p := writeEnv(t)
	var calls [][]string
	w := NewWeb("tok", p, func(args []string) (string, error) {
		calls = append(calls, args)
		return "ok", nil
	})
	w.Flags = testFlags
	return w, &calls
}

func post(h http.Handler, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	if token != "" {
		r.Header.Set("X-Token", token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestWebTokenRequired(t *testing.T) {
	w, calls := webFixture(t)
	h := w.Handler()
	for _, path := range []string{"/", "/api/model"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without token: %d", path, rec.Code)
		}
	}
	if rec := post(h, "/api/apply", "wrong", `{"values":{"ishv":"false"}}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("apply wrong token: %d", rec.Code)
	}
	if len(*calls) != 0 {
		t.Fatal("apply ran without a token")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?token=tok", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "skywire autoconfig") {
		t.Fatalf("page: %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "http://") || strings.Contains(rec.Body.String(), "https://") {
		t.Fatal("page references an external URL")
	}
}

func TestWebModelPrintApply(t *testing.T) {
	w, calls := webFixture(t)
	h := w.Handler()

	r := httptest.NewRequest(http.MethodGet, "/api/model", nil)
	r.Header.Set("X-Token", "tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	var mr response
	if err := json.Unmarshal(rec.Body.Bytes(), &mr); err != nil || mr.Model == nil || len(mr.Model.Fields) == 0 {
		t.Fatalf("model: %v %s", err, rec.Body.String())
	}

	rec = post(h, "/api/print", "tok", `{"values":{"rewardaddr":"a b","ishv":"false"}}`)
	var pr response
	if err := json.Unmarshal(rec.Body.Bytes(), &pr); err != nil {
		t.Fatal(err)
	}
	if pr.Command != "skywire autoconfig --no-ishv --rewardaddr='a b'" {
		t.Fatalf("print: %q (%s)", pr.Command, rec.Body.String())
	}
	if len(*calls) != 0 {
		t.Fatal("print applied")
	}

	rec = post(h, "/api/apply", "tok", `{"values":{"min-hops":"4"},"no_restart":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body.String())
	}
	if len(*calls) != 1 || strings.Join((*calls)[0], " ") != "--min-hops=4 --no-restart" {
		t.Fatalf("calls %v", *calls)
	}

	if rec = post(h, "/api/apply", "tok", `{"values":{"min-hops":"x"}}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad int: %d", rec.Code)
	}
	if rec = post(h, "/api/apply", "tok", `{"values":{"nope":"x"}}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown: %d", rec.Code)
	}
	if len(*calls) != 1 {
		t.Fatal("invalid request applied")
	}
}

func mustSet(t *testing.T, m *Model, name, v string) {
	t.Helper()
	if err := m.Set(name, v); err != nil {
		t.Fatal(err)
	}
}

func mustArgs(t *testing.T, m *Model) []string {
	t.Helper()
	a, err := m.Args()
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func mustCommand(t *testing.T, m *Model) string {
	t.Helper()
	c, err := m.Command()
	if err != nil {
		t.Fatal(err)
	}
	return c
}
