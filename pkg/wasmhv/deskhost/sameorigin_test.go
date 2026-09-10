package deskhost

import "testing"

// TestSameOriginSrc pins the browser role's DirectLoader rule: pages of the
// serving origin render natively, anything else goes through the transcoder.
// A claimed page is rendered UNSANDBOXED, so the negatives matter as much as
// the positives — a prefix match must not be fooled by a longer port or a
// look-alike host.
func TestSameOriginSrc(t *testing.T) {
	const origin = "http://127.0.0.1:8000"
	for _, in := range []string{
		"http://127.0.0.1:8000",
		"http://127.0.0.1:8000/",
		"http://127.0.0.1:8000/?embed=1#/?embed=1",
		"http://127.0.0.1:8000/pty/0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c",
		"http://127.0.0.1:8000?embed=1",
		"http://127.0.0.1:8000#/nodes",
	} {
		src, ok := sameOriginSrc(origin, in)
		if !ok || src != in {
			t.Errorf("sameOriginSrc(%q) = (%q,%v), want (%q,true)", in, src, ok, in)
		}
	}
	for _, in := range []string{
		"http://127.0.0.1:80001/",         // longer port sharing the prefix
		"http://127.0.0.1:8000.evil.com/", // look-alike host
		"https://127.0.0.1:8000/",         // other scheme
		"http://localhost:8000/",          // other spelling of the same host is NOT the origin string
		"http://example.com/",
		"vnet:8001",
		"",
	} {
		if src, ok := sameOriginSrc(origin, in); ok {
			t.Errorf("sameOriginSrc(%q) claimed %q, want no claim", in, src)
		}
	}
	if _, ok := sameOriginSrc("", "http://127.0.0.1:8000/"); ok {
		t.Error("an empty origin must claim nothing")
	}
}
