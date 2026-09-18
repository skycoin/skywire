package visor

import "testing"

func TestNormalizeVOrigins(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		wantErr  bool
	}{
		{in: "https://app.example", want: "https://app.example"},
		{in: "https://a.example,https://b.example", want: "https://a.example,https://b.example"},
		{in: " https://a.example , https://b.example ", want: "https://a.example,https://b.example"},
		{in: "https://a.example,https://a.example", want: "https://a.example"},
		{in: "http://localhost:8000", want: "http://localhost:8000"},
		{in: "*", want: "*"},
		// One "*" settles it: listing origins beside it would read as a
		// restriction that is not being applied.
		{in: "https://a.example,*", want: "*"},
		{in: "https://app.example/", want: "https://app.example"},

		{in: "", wantErr: true},
		{in: "   ", wantErr: true},
		{in: "app.example", wantErr: true},              // no scheme
		{in: "https://app.example/path", wantErr: true}, // an origin has no path
		{in: "https://app.example?x=1", wantErr: true},
		{in: "https://", wantErr: true},
	} {
		got, err := normalizeVOrigins(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("normalizeVOrigins(%q) = %q, want an error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeVOrigins(%q) errored: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("normalizeVOrigins(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
