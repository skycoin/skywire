// Package httpauth pkg/httpauth/redis-store_test.go
package httpauth

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/httputil"
)

// fakeRedis answers every command on an in-memory conn with reply, a raw RESP
// frame.
func fakeRedis(reply string) func(context.Context, string, string) (net.Conn, error) {
	return func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close() //nolint:errcheck
			rd := bufio.NewReader(server)
			for {
				// Every command is an array header ("*N") followed by N
				// bulk strings, each a "$len" line and a data line.
				hdr, err := rd.ReadString('\n')
				if err != nil {
					return
				}
				n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(hdr, "*")))
				if err != nil {
					return
				}
				for i := 0; i < 2*n; i++ {
					if _, err := rd.ReadString('\n'); err != nil {
						return
					}
				}
				if _, err := server.Write([]byte(reply)); err != nil {
					return
				}
			}
		}()
		return client, nil
	}
}

func testRedisStore(t *testing.T, dialer func(context.Context, string, string) (net.Conn, error)) *redisStore {
	t.Helper()
	cl := redis.NewClient(&redis.Options{Addr: "fake:6379", Dialer: dialer, MaxRetries: -1})
	t.Cleanup(func() { cl.Close() }) //nolint:errcheck,gosec
	return &redisStore{client: cl}
}

func TestRedisStore_Nonce(t *testing.T) {
	ctx := context.Background()

	t.Run("missing key is nonce 0", func(t *testing.T) {
		s := testRedisStore(t, fakeRedis("$-1\r\n"))
		n, err := s.Nonce(ctx, testPubKey)
		require.NoError(t, err)
		assert.Equal(t, Nonce(0), n)
	})

	t.Run("stored nonce", func(t *testing.T) {
		s := testRedisStore(t, fakeRedis("$2\r\n42\r\n"))
		n, err := s.Nonce(ctx, testPubKey)
		require.NoError(t, err)
		assert.Equal(t, Nonce(42), n)
	})

	t.Run("backend error is an error, not nonce 0", func(t *testing.T) {
		down := errors.New("connection refused")
		s := testRedisStore(t, func(context.Context, string, string) (net.Conn, error) {
			return nil, down
		})
		_, err := s.Nonce(ctx, testPubKey)
		require.ErrorIs(t, err, down)
	})

	t.Run("server error reply is an error", func(t *testing.T) {
		s := testRedisStore(t, fakeRedis("-LOADING dataset in memory\r\n"))
		_, err := s.Nonce(ctx, testPubKey)
		require.Error(t, err)
	})
}

// A nonce store that cannot be read is a server fault: the request must fail
// with a 5xx, never be checked against nonce 0.
func TestWithAuth_StoreErrorIs5xx(t *testing.T) {
	store := newMemoryStore()
	store.SetError(errors.New("redis down"))

	h := WithAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httputil.WriteJSON(w, r, http.StatusOK, "")
	}), true)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/foo", bytes.NewReader([]byte("hi")))
	r.Header = validHeaders(t, []byte("hi"))
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
}
