package ioutil

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBufRead_ExactFit(t *testing.T) {
	buf := new(bytes.Buffer)
	data := []byte("hello")
	p := make([]byte, 5)

	n, err := BufRead(buf, data, p)
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, []byte("hello"), p)
	assert.Equal(t, 0, buf.Len())
}

func TestBufRead_ShortP(t *testing.T) {
	buf := new(bytes.Buffer)
	data := []byte("hello world")
	p := make([]byte, 5)

	n, err := BufRead(buf, data, p)
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, []byte("hello"), p)
	assert.Equal(t, " world", buf.String())
}

func TestBufRead_EmptyData(t *testing.T) {
	buf := new(bytes.Buffer)
	data := []byte{}
	p := make([]byte, 10)

	n, err := BufRead(buf, data, p)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
	assert.Equal(t, 0, buf.Len())
}

func TestBufRead_EmptyP(t *testing.T) {
	buf := new(bytes.Buffer)
	data := []byte("hello")
	p := make([]byte, 0)

	n, err := BufRead(buf, data, p)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
	assert.Equal(t, "hello", buf.String())
}

func TestBufRead_LargeData(t *testing.T) {
	buf := new(bytes.Buffer)
	data := make([]byte, 10000)
	for i := range data {
		data[i] = byte(i % 256)
	}
	p := make([]byte, 100)

	n, err := BufRead(buf, data, p)
	require.NoError(t, err)
	assert.Equal(t, 100, n)
	assert.Equal(t, data[:100], p)
	assert.Equal(t, 9900, buf.Len())
	assert.Equal(t, data[100:], buf.Bytes())
}
