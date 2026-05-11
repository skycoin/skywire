package clicache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPutGet_RoundTrip(t *testing.T) {
	c, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close() //nolint:errcheck,gosec

	url := "http://sd.example.com/api/services?type=visor"
	body := []byte(`[{"address":"deadbeef:80","type":"visor"}]`)
	if err := c.Put(url, body); err != nil {
		t.Fatal(err)
	}
	got, ok := c.Get(url)
	if !ok {
		t.Fatalf("Get miss")
	}
	if string(got.Body) != string(body) {
		t.Errorf("body = %q, want %q", got.Body, body)
	}
	if time.Since(got.FetchedAt) > time.Minute {
		t.Errorf("fetched_at too far in past: %v", got.FetchedAt)
	}
}

func TestFresh_RespectsMaxAge(t *testing.T) {
	c, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close() //nolint:errcheck,gosec

	url := "http://sd.example.com/api/services?type=visor"
	if err := c.Put(url, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Fresh(url, time.Minute); !ok {
		t.Errorf("Fresh miss on just-put entry")
	}
	if _, ok := c.Fresh(url, time.Nanosecond); ok {
		t.Errorf("Fresh hit on too-strict maxAge")
	}
}

func TestGet_MissOnUnknownURL(t *testing.T) {
	c, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close() //nolint:errcheck,gosec

	if _, ok := c.Get("http://nope"); ok {
		t.Errorf("Get hit on never-put URL")
	}
}

func TestBucketForURL_PerHost(t *testing.T) {
	for _, tc := range []struct{ url, want string }{
		{"http://sd.skycoin.com/api/services", "sd.skycoin.com"},
		{"http://tpd.skywire.skycoin.com/all-transports", "tpd.skywire.skycoin.com"},
		{"not a url", "_"},
		{"", "_"},
	} {
		if got := bucketForURL(tc.url); got != tc.want {
			t.Errorf("bucketForURL(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

func TestNilReceiver_Safe(t *testing.T) {
	var c *Cache
	if _, ok := c.Get("anything"); ok {
		t.Errorf("Get on nil cache returned ok")
	}
	if _, ok := c.Fresh("anything", time.Minute); ok {
		t.Errorf("Fresh on nil cache returned ok")
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close on nil cache returned %v", err)
	}
	if c.Path() != "" {
		t.Errorf("Path on nil cache returned %q", c.Path())
	}
}

func TestOpen_ChmodFilePermissions(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	c, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close() //nolint:errcheck,gosec
	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// 0o666 may be reduced by extreme umasks like 077, but in normal
	// environments at least other-read should survive. We assert the
	// mode is *at least* 0o644 to keep the test stable on CI sandboxes.
	if mode := info.Mode().Perm(); mode < 0o644 {
		t.Errorf("DB perms = %#o, want >= 0o644", mode)
	}
}
