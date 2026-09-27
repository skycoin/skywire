// Package buildinfo pkg/buildinfo/buildinfo_test.go c0-com-util
package buildinfo

import "testing"

// TestGetNamesARelease: a tagged release reports its tag alone, which is what
// operators and `svc health` compare against; v1.3.95 and v1.3.96 reported
// "v1.3.96-a44e1e6e851d". Anything else still carries its commit.
func TestGetNamesARelease(t *testing.T) {
	defer func(v, c string) { version, commit = v, c }(version, commit)
	commit = "a44e1e6e851d117e8e47c34991d6c980170a402b"

	for _, tc := range []struct{ version, want string }{
		{"v1.3.96", "v1.3.96"},
		{"v1.3.96+dirty", "v1.3.96+dirty-a44e1e6e851d"},
		{"v1.3.97-0.20260927150000-a44e1e6e851d", "v1.3.97-0.20260927150000-a44e1e6e851d"},
		{"v1.3.97-rc1", "v1.3.97-rc1-a44e1e6e851d"},
	} {
		version = tc.version
		if got := Get().Version; got != tc.want {
			t.Errorf("version %q: Get().Version = %q, want %q", tc.version, got, tc.want)
		}
	}
}
