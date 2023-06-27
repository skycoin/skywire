//go:build !no_ci
// +build !no_ci

package store

import (
	"testing"
)

func TestMemory(t *testing.T) {
	s := newMemoryStore()
	testNetwork(t, s)
}
