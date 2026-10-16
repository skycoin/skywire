package visor

import "testing"

func TestBrowserHostPort(t *testing.T) {
	for addr, want := range map[string]string{
		":7999":          "localhost:7999",
		"0.0.0.0:7999":   "localhost:7999",
		"127.0.0.1:8446": "127.0.0.1:8446",
		"[::]:7999":      "localhost:7999",
	} {
		if got := browserHostPort(addr); got != want {
			t.Errorf("browserHostPort(%q) = %q, want %q", addr, got, want)
		}
	}
}
