package skyobject

import (
	"testing"
)

// TestNewConfigPopulatesMaxFillingParallel guards against the
// regression where NewConfig() left MaxFillingParallel at its int
// zero value, which then flowed through to (*Node).maxFillingParallel
// and triggered the magic-1024 fallback in pkg/cxo/node/head.go's
// maxParallel(), pinning thousands of goroutines parked in
// (*Filler).get's select. The documented default is the package
// constant MaxFillingParallel = 10.
func TestNewConfigPopulatesMaxFillingParallel(t *testing.T) {
	c := NewConfig()
	if c.MaxFillingParallel != MaxFillingParallel {
		t.Fatalf("NewConfig().MaxFillingParallel = %d, want %d (the documented constant)",
			c.MaxFillingParallel, MaxFillingParallel)
	}
	if c.MaxFillingParallel <= 0 {
		t.Fatalf("NewConfig().MaxFillingParallel must be > 0 to avoid the 1024 fallback in node/head.go, got %d",
			c.MaxFillingParallel)
	}
}
