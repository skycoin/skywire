package visor

import "testing"

func TestArgsListenOn(t *testing.T) {
	for _, c := range []struct {
		args []string
		port string
		want bool
	}{
		{[]string{"app", "skysocks-client", "--addr", "127.0.0.1:1085"}, "1085", true},
		{[]string{"--addr=:1080", "--reconnect"}, "1080", true},
		{[]string{"--addr", ":1080"}, "1085", false},
		{[]string{"--addr"}, "1080", false},
		{nil, "1080", false},
	} {
		if got := argsListenOn(c.args, c.port); got != c.want {
			t.Errorf("argsListenOn(%q, %s) = %v, want %v", c.args, c.port, got, c.want)
		}
	}
}
