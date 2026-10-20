package pty

import (
	"net/http/httptest"
	"testing"
)

func TestPageTarget(t *testing.T) {
	for _, tc := range []struct{ url, want string }{
		{"/pty", "./pty"},
		{"/pty/", "./"},
		{"/pty/0281a102", "./0281a102"},
		{"/pty/0281a102?commands=update&asset=term.wasm", "./0281a102?commands=update"},
		{"/pty/x?sid=old", "./x"},
		{"/pty/wss:%2F%2Fevil.example", "./evil.example"},
	} {
		if got := pageTarget(httptest.NewRequest("GET", tc.url, nil)); got != tc.want {
			t.Errorf("pageTarget(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"./pty":         "'./pty'",
		"./a;rm -rf ~":  "'./a;rm -rf ~'",
		"./it's":        `'./it'\''s'`,
		"./x?a=$(echo)": "'./x?a=$(echo)'",
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
