package visorconfig

import (
	"encoding/json"
	"testing"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/routing"
)

func TestSplitArgsRoundTrip(t *testing.T) {
	cases := [][]string{
		nil,
		{"skycoin", "daemon"},
		{"skycoin", "daemon", "--port", "6420", "--data-dir", "/var/skycoin/sky"},
		{"--node-url", "http://127.0.0.1:6420"},
		// tokens with spaces
		{"--label", "my visor", "--addr", "127.0.0.1:8001"},
		// tokens with quotes
		{`--note`, `"hello"`},
		{"--", "passthrough", "with spaces"},
	}
	for _, in := range cases {
		s := joinArgs(in)
		out, err := splitArgs(s)
		if err != nil {
			t.Fatalf("split %q: %v", s, err)
		}
		if !slicesEq(in, out) {
			t.Fatalf("round-trip differs:\n in=%q\n out=%q\n via=%q", in, out, s)
		}
	}
}

func slicesEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAppsListAcceptsBothForms(t *testing.T) {
	// New string form
	stringForm := []byte(`[{"name":"d","binary":"x","args":"a b c","auto_start":true,"port":1}]`)
	var a appsList
	if err := json.Unmarshal(stringForm, &a); err != nil {
		t.Fatalf("string-form unmarshal: %v", err)
	}
	if len(a) != 1 || !slicesEq(a[0].Args, []string{"a", "b", "c"}) {
		t.Fatalf("string-form args: got %#v", a[0].Args)
	}

	// Legacy array form
	arrayForm := []byte(`[{"name":"d","binary":"x","args":["a","b","c"],"auto_start":true,"port":1}]`)
	var b appsList
	if err := json.Unmarshal(arrayForm, &b); err != nil {
		t.Fatalf("array-form unmarshal: %v", err)
	}
	if len(b) != 1 || !slicesEq(b[0].Args, []string{"a", "b", "c"}) {
		t.Fatalf("array-form args: got %#v", b[0].Args)
	}
}

func TestAppsListEmitsString(t *testing.T) {
	a := appsList{appserver.AppConfig{
		Name:      "skycoin-daemon-aix",
		Binary:    "skycoin-daemon",
		Args:      []string{"skycoin", "daemon", "--port", "6422"},
		AutoStart: true,
		Port:      routing.Port(61),
	}}
	out, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(out)
	want := `[{"name":"skycoin-daemon-aix","binary":"skycoin-daemon","args":"skycoin daemon --port 6422","auto_start":true,"port":61}]`
	if got != want {
		t.Fatalf("marshal output:\n got=%s\n want=%s", got, want)
	}
}
