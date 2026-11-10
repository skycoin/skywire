package alerts

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	skycipher "github.com/skycoin/skywire/pkg/cipher"
)

func mustB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := b64(s)
	require.NoError(t, err)
	return b
}

// TestEncryptPushRFC8291 checks the encryption against RFC 8291
// Appendix A.
func TestEncryptPushRFC8291(t *testing.T) {
	asKey, err := ecdh.P256().NewPrivateKey(mustB64(t, "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	require.NoError(t, err)
	require.Equal(t, "BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8",
		base64.RawURLEncoding.EncodeToString(asKey.PublicKey().Bytes()))
	sub := PushSubscription{
		Endpoint: "https://push.example.net/push/JzLQ3raZJfFBR0aqvOMsLrt54w4rJUsV",
		Keys: PushKeys{
			P256DH: "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4",
			Auth:   "BTBZMqHH6r4Tts7J_aSIgg",
		},
	}
	require.NoError(t, sub.Validate())
	got, err := encryptPush(sub, []byte("When I grow up, I want to be a watermelon"), asKey, mustB64(t, "DGv6ra1nlYgDCS1FRnbzlw"))
	require.NoError(t, err)
	require.Equal(t, "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN",
		base64.RawURLEncoding.EncodeToString(got))
}

// decryptPush is the browser side of RFC 8291, for the tests.
func decryptPush(t *testing.T, ua *ecdh.PrivateKey, auth, body []byte) []byte {
	t.Helper()
	salt, rs, idlen := body[:16], binary.BigEndian.Uint32(body[16:20]), int(body[20])
	require.Equal(t, uint32(pushRecordSize), rs)
	asRaw := body[21 : 21+idlen]
	asPub, err := ecdh.P256().NewPublicKey(asRaw)
	require.NoError(t, err)
	shared, err := ua.ECDH(asPub)
	require.NoError(t, err)
	info := append(append([]byte("WebPush: info\x00"), ua.PublicKey().Bytes()...), asRaw...)
	ikm := hkdf(auth, shared, info, 32)
	block, err := aes.NewCipher(hkdf(salt, ikm, []byte("Content-Encoding: aes128gcm\x00"), 16))
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	pt, err := gcm.Open(nil, hkdf(salt, ikm, []byte("Content-Encoding: nonce\x00"), 12), body[21+idlen:], nil)
	require.NoError(t, err)
	require.Equal(t, byte(2), pt[len(pt)-1])
	return pt[:len(pt)-1]
}

// checkVAPID verifies an RFC 8292 Authorization header and returns its
// claims.
func checkVAPID(t *testing.T, h string) map[string]any {
	t.Helper()
	require.True(t, strings.HasPrefix(h, "vapid t="), h)
	tok, k, ok := strings.Cut(strings.TrimPrefix(h, "vapid t="), ", k=")
	require.True(t, ok)
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), mustB64(t, k))
	require.NoError(t, err)
	parts := strings.Split(tok, ".")
	require.Len(t, parts, 3)
	sig := mustB64(t, parts[2])
	require.Len(t, sig, 64)
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	require.True(t, ecdsa.Verify(pub, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])), "bad VAPID signature")
	var claims map[string]any
	require.NoError(t, json.Unmarshal(mustB64(t, parts[1]), &claims))
	return claims
}

type browser struct {
	key  *ecdh.PrivateKey
	auth []byte
}

func newBrowser(t *testing.T) browser {
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	return browser{key: k, auth: []byte("0123456789abcdef")}
}

func (b browser) sub(endpoint, label string) PushSubscription {
	enc := base64.RawURLEncoding
	return PushSubscription{Endpoint: endpoint, Label: label,
		Keys: PushKeys{P256DH: enc.EncodeToString(b.key.PublicKey().Bytes()), Auth: enc.EncodeToString(b.auth)}}
}

// TestServicePush subscribes two browsers to a fake push service, one of
// which the service has dropped, and checks delivery, the VAPID token,
// the payload and the removal of the dropped one.
func TestServicePush(t *testing.T) {
	br := newBrowser(t)
	var mu sync.Mutex
	var got []PushPayload
	var hdrs []http.Header
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/gone" {
			w.WriteHeader(http.StatusGone)
			return
		}
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var p PushPayload
		require.NoError(t, json.Unmarshal(decryptPush(t, br.key, br.auth, body), &p))
		mu.Lock()
		got = append(got, p)
		hdrs = append(hdrs, r.Header.Clone())
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	dir := t.TempDir()
	watcher, _ := skycipher.GenerateKeyPair()
	sender := &Sender{Watcher: watcher, HTTPClient: srv.Client()}
	svc, err := NewService(dir, nil, sender)
	require.NoError(t, err)
	pub := svc.Status().PushPublicKey
	require.NotEmpty(t, pub)

	_, err = svc.PushAdd(PushSubscription{Endpoint: "http://insecure.example/x", Keys: br.sub("", "").Keys})
	require.Error(t, err, "plain http endpoint")
	a, err := svc.PushAdd(br.sub(srv.URL+"/ok", "phone"))
	require.NoError(t, err)
	again, err := svc.PushAdd(br.sub(srv.URL+"/ok", "phone again"))
	require.NoError(t, err)
	require.Equal(t, a.ID, again.ID)
	_, err = svc.PushAdd(br.sub(srv.URL+"/gone", "old"))
	require.NoError(t, err)
	st := svc.Status()
	require.True(t, st.Enabled)
	require.Len(t, st.Settings.Push, 2)
	require.Empty(t, st.Settings.Push[0].Keys.Auth, "reads carry no keys")

	// Settings writes from a read keep the subscriptions.
	require.NoError(t, svc.SetSettings(st.Settings))
	require.Len(t, svc.Settings().Push, 2)
	require.NotEmpty(t, svc.Settings().Push[0].Keys.Auth)

	res, err := svc.Test(context.Background())
	require.NoError(t, err)
	require.Len(t, res, 2)
	require.Empty(t, res[0].Err)
	require.Contains(t, res[1].Err, "410")
	require.Len(t, svc.Settings().Push, 1, "the gone device is removed")

	mu.Lock()
	require.Len(t, got, 1)
	require.Equal(t, KindTest, got[0].Event)
	require.Contains(t, got[0].Body, "test of your skywire visor alerts")
	require.Equal(t, watcher.String(), got[0].Watcher)
	claims := checkVAPID(t, hdrs[0].Get("Authorization"))
	require.Contains(t, hdrs[0].Get("Authorization"), "k="+pub)
	require.Equal(t, "aes128gcm", hdrs[0].Get("Content-Encoding"))
	require.NotEmpty(t, hdrs[0].Get("TTL"))
	mu.Unlock()
	require.Equal(t, srv.URL, claims["aud"])
	require.Equal(t, DefaultPushSubject, claims["sub"])
	require.InDelta(t, time.Now().Add(time.Hour).Unix(), claims["exp"], 60)

	// An offline alert is urgent and names the visor.
	pk, _ := skycipher.GenerateKeyPair()
	online := false
	svc.Targets = func(context.Context, Settings) []Target {
		return []Target{{PK: pk, Hostname: "board-1", Online: online}}
	}
	require.NoError(t, svc.SetSettings(Settings{OfflineAfter: Duration(time.Minute)}))
	t0 := time.Now()
	online = true
	svc.Tick(context.Background(), t0)
	online = false
	svc.Tick(context.Background(), t0.Add(30*time.Second))
	svc.Tick(context.Background(), t0.Add(100*time.Second))
	mu.Lock()
	require.Len(t, got, 2)
	require.Equal(t, KindOffline, got[1].Event)
	require.Equal(t, pk.String(), got[1].PK)
	require.Contains(t, got[1].Title, "is offline")
	require.Contains(t, got[1].Body, "board-1")
	require.Equal(t, "high", hdrs[1].Get("Urgency"))
	mu.Unlock()

	// The key survives a restart.
	svc2, err := NewService(dir, nil, sender)
	require.NoError(t, err)
	require.Equal(t, pub, svc2.Status().PushPublicKey)
	require.Len(t, svc2.Settings().Push, 1)
	_, err = os.Stat(filepath.Join(dir, vapidFile))
	require.NoError(t, err)

	n, err := svc2.PushRemove("all")
	require.NoError(t, err)
	require.Equal(t, 1, n)
	_, err = svc2.PushRemove(a.ID)
	require.Error(t, err)
}
