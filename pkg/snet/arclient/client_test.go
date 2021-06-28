package arclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/go-chi/chi"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/internal/httpauth"
)

func TestClientAuth(t *testing.T) {
	testPubKey, testSecKey := cipher.GenerateKeyPair()

	wg := sync.WaitGroup{}

	headerCh := make(chan http.Header, 1)
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			switch url := r.URL.String(); url {
			case "/":
				defer wg.Done()
				headerCh <- r.Header

			case fmt.Sprintf("/security/nonces/%s", testPubKey):
				if _, err := fmt.Fprintf(w, `{"edge": "%s", "next_nonce": 1}`, testPubKey); err != nil {
					t.Errorf("Failed to write nonce response: %w", err)
				}

			default:
				t.Errorf("Don't know how to handle URL = '%s'", url)
			}
		},
	))

	defer srv.Close()
	log := logging.MustGetLogger("test_client_auth")
	apiClient, err := NewHTTP(srv.URL, testPubKey, testSecKey, log)
	require.NoError(t, err)

	c := apiClient.(*httpClient)

	wg.Add(1)

	resp, err := c.Get(context.TODO(), "/")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	header := <-headerCh
	assert.Equal(t, testPubKey.Hex(), header.Get("SW-Public"))
	assert.Equal(t, "1", header.Get("SW-Nonce"))
	assert.NotEmpty(t, header.Get("SW-Sig")) // TODO: check for the right key

	wg.Wait()
}

func TestBind(t *testing.T) {
	testPubKey, testSecKey := cipher.GenerateKeyPair()

	urlCh := make(chan string, 1)
	srv := httptest.NewServer(authHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		urlCh <- r.URL.String()
	})))

	defer srv.Close()
	log := logging.MustGetLogger("test_bind")
	c, err := NewHTTP(srv.URL, testPubKey, testSecKey, log)
	require.NoError(t, err)

	err = c.BindSTCPR(context.TODO(), "1234")
	require.NoError(t, err)

	assert.Equal(t, "/bind/stcpr", <-urlCh)
}

func authHandler(next http.Handler) http.Handler {
	log := logging.MustGetLogger("arclient_test")
	testPubKey, _ := cipher.GenerateKeyPair()
	r := chi.NewRouter()

	r.Handle("/security/nonces/{pk}", http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewEncoder(w).Encode(&httpauth.NextNonceResponse{Edge: testPubKey, NextNonce: 1}); err != nil {
				log.WithError(err).Error("Failed to encode nonce response")
			}
		},
	))

	r.Handle("/*", next)

	return r
}
