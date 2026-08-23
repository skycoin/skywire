// Package cmdutil pkg/cmdutil/climisc_test.go c0-com-util
package cmdutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExampleJSON(t *testing.T) {
	assert.Contains(t, ExampleJSON(map[string]string{"k": "v"}), "\"k\"")
	// a value that cannot be marshaled yields the empty string, not a panic.
	assert.Equal(t, "", ExampleJSON(make(chan int)))
}

func TestCommaSplit(t *testing.T) {
	assert.Nil(t, CommaSplit(""))
	assert.Equal(t, []string{"a", "b", "c"}, CommaSplit("a, b ,c"))
	assert.Equal(t, []string{"x"}, CommaSplit(" x "))
	// all-blank-after-trim yields a non-nil but empty slice.
	assert.Empty(t, CommaSplit(" , , "))
}
