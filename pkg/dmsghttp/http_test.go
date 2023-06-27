// Package dmsghttp pkg/dmsghttp/http_test.go
package dmsghttp

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/stretchr/testify/assert"

	dmsg "github.com/skycoin/skywire/pkg/dmsg"
)

const (
	endpointHTML = "/index.html"
	endpointEcho = "/echo"
	endpointHash = "/hash"
)

var endpointHTMLData = []byte("<html><body><h1>Hello World!</h1></body></html>")

type httpServerResult struct {
	Path string

	ReqB   []byte
	ReqErr error

	RespB   []byte
	RespErr error
}

func (r httpServerResult) Assert(t *testing.T, i int) {
	t.Logf("[%d] Asserting httpServerResult result: %v", i, r)
	assert.NoError(t, r.ReqErr, i)
	assert.NoError(t, r.RespErr, i)

	switch r.Path {
	case endpointHTML:
		assert.Equal(t, r.RespB, endpointHTMLData, i)
	case endpointEcho:
		assert.Equal(t, r.RespB, r.ReqB, i)
	case endpointHash:
		hash := cipher.SumSHA256(r.ReqB)
		assert.Equal(t, r.RespB, hash[:], i)
	default:
		t.Errorf("Invalid endpoint path: %s", r.Path)
	}
}

type httpClientResult struct {
	Path string

	ReqB   []byte
	ReqErr error

	RespB        []byte
	RespErr      error
	RespCloseErr error
}

func (r httpClientResult) Assert(t *testing.T, i int) {
	t.Logf("[%d] Asserting httpClientResult result: %v", i, r)

	assert.NoError(t, r.ReqErr, i)
	assert.NoError(t, r.RespErr, i)
	assert.NoError(t, r.RespCloseErr, i)

	switch r.Path {
	case endpointHTML:
		assert.Equal(t, r.RespB, endpointHTMLData, i)
	case endpointEcho:
		assert.Equal(t, r.ReqB, r.RespB, i)
	case endpointHash:
		hash := cipher.SumSHA256(r.ReqB)
		assert.Equal(t, hash[:], r.RespB, i)
	default:
		t.Errorf("Invalid endpoint path: %s", r.Path)
	}
}

func startHTTPServer(t *testing.T, results chan httpServerResult, lis net.Listener) {
	r := chi.NewRouter()

	r.HandleFunc(endpointHTML, func(w http.ResponseWriter, r *http.Request) {
		result := httpServerResult{Path: endpointHTML}

		n, err := w.Write(endpointHTMLData)
		result.RespB = endpointHTMLData[:n]
		result.RespErr = err

		results <- result
	})

	r.HandleFunc(endpointEcho, func(w http.ResponseWriter, r *http.Request) {
		result := httpServerResult{Path: endpointEcho}

		data, err := io.ReadAll(r.Body)
		result.ReqB = data
		result.ReqErr = err

		n, err := w.Write(data)
		result.RespB = data[:n]
		result.RespErr = err

		results <- result
	})

	r.HandleFunc(endpointHash, func(w http.ResponseWriter, r *http.Request) {
		result := httpServerResult{Path: endpointHash}

		data, err := io.ReadAll(r.Body)
		result.ReqB = data
		result.ReqErr = err

		hash := cipher.SumSHA256(data)

		n, err := w.Write(hash[:])
		result.RespB = hash[:n]
		result.RespErr = err

		results <- result
	})

	errCh := make(chan error, 1)
	go func() {
		srv := &http.Server{
			ReadTimeout:       3 * time.Second,
			WriteTimeout:      3 * time.Second,
			IdleTimeout:       30 * time.Second,
			ReadHeaderTimeout: 3 * time.Second,
			Handler:           r,
		}
		errCh <- srv.Serve(lis)
		close(errCh)
	}()

	t.Cleanup(func() {
		assert.NoError(t, lis.Close())
		assert.EqualError(t, <-errCh, dmsg.ErrEntityClosed.Error())
	})
}

func requestHTTP(httpC *http.Client, method, url string, body []byte) httpClientResult {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		panic(err) // Only happens due to malformed test.
	}

	result := httpClientResult{Path: req.URL.Path, ReqB: body}

	resp, reqErr := httpC.Do(req)
	if reqErr != nil {
		result.ReqErr = reqErr
		return result
	}

	b, respErr := io.ReadAll(resp.Body)
	result.RespB = b
	result.ReqErr = respErr
	result.RespCloseErr = resp.Body.Close()

	return result
}
