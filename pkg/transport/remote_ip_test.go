package transport

import "testing"

func TestHostOfRawAddr(t *testing.T) {
	for in, want := range map[string]string{
		"1.2.3.4:5000":                        "1.2.3.4",
		"[2001:db8::1]:443":                   "2001:db8::1",
		"https://193.24.233.37:36011/skywire": "193.24.233.37",
		"wss://dmsg1.example.net/dmsg":        "dmsg1.example.net",
		"ws://75.220.70.207:41281/":           "75.220.70.207",
		"https://[2001:db8::2]:7779/skywire":  "2001:db8::2",
		"":                                    "",
		"not an address":                      "",
	} {
		if got := hostOfRawAddr(in); got != want {
			t.Errorf("hostOfRawAddr(%q) = %q, want %q", in, got, want)
		}
	}
}
