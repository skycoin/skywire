package disc_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

// --- Mock client Entry/PostEntry/PutEntry/DelEntry lifecycle ---

func TestMockClientEntryLifecycle(t *testing.T) {
	ctx := context.Background()
	mock := disc.NewMock(0)

	pk, sk := cipher.GenerateKeyPair()

	// Entry should not exist yet.
	_, err := mock.Entry(ctx, pk)
	require.Error(t, err, "entry should not exist before being posted")

	// Create and post a server entry.
	entry := disc.NewServerEntry(pk, 0, "localhost:9090", 10)
	require.NoError(t, entry.Sign(sk))
	require.NoError(t, mock.PostEntry(ctx, entry))

	// Retrieve and verify.
	got, err := mock.Entry(ctx, pk)
	require.NoError(t, err)
	assert.Equal(t, pk, got.Static)
	assert.Equal(t, "localhost:9090", got.Server.Address)

	// Update via PutEntry.
	entry.Server.Address = "remotehost:9091"
	require.NoError(t, mock.PutEntry(ctx, sk, entry))

	got2, err := mock.Entry(ctx, pk)
	require.NoError(t, err)
	assert.Equal(t, "remotehost:9091", got2.Server.Address)
	assert.Greater(t, got2.Sequence, got.Sequence)

	// Delete entry.
	require.NoError(t, mock.DelEntry(ctx, entry))

	_, err = mock.Entry(ctx, pk)
	require.Error(t, err, "entry should be gone after deletion")
}

func TestMockClientPostEntryValidatesIteration(t *testing.T) {
	ctx := context.Background()
	mock := disc.NewMock(0)

	pk, sk := cipher.GenerateKeyPair()

	// Post initial entry at sequence 0.
	entry := disc.NewServerEntry(pk, 0, "addr:1234", 5)
	require.NoError(t, entry.Sign(sk))
	require.NoError(t, mock.PostEntry(ctx, entry))

	// Posting again with same sequence should fail (wrong sequence).
	entry2 := disc.NewServerEntry(pk, 0, "addr:1234", 5)
	require.NoError(t, entry2.Sign(sk))
	err := mock.PostEntry(ctx, entry2)
	require.Error(t, err)
}

func TestMockClientPutEntryWrongKey(t *testing.T) {
	ctx := context.Background()
	mock := disc.NewMock(0)

	pk, sk := cipher.GenerateKeyPair()
	_, wrongSk := cipher.GenerateKeyPair()

	entry := disc.NewServerEntry(pk, 0, "addr:5555", 2)
	require.NoError(t, entry.Sign(sk))
	require.NoError(t, mock.PostEntry(ctx, entry))

	// PutEntry with wrong secret key should fail.
	entry.Server.Address = "addr:6666"
	err := mock.PutEntry(ctx, wrongSk, entry)
	require.Error(t, err)
}

func TestMockClientDelEntryNonExistent(t *testing.T) {
	ctx := context.Background()
	mock := disc.NewMock(0)

	pk, _ := cipher.GenerateKeyPair()
	entry := disc.NewServerEntry(pk, 0, "addr:1111", 1)

	// Deleting non-existent entry should not error (mock behavior).
	require.NoError(t, mock.DelEntry(ctx, entry))
}

// --- AvailableServers / AllServers with mock ---

func TestMockAvailableServersEmpty(t *testing.T) {
	ctx := context.Background()
	mock := disc.NewMock(0)

	servers, err := mock.AvailableServers(ctx)
	require.NoError(t, err)
	assert.Empty(t, servers)
}

func TestMockAvailableServersOnlyReturnsServerEntries(t *testing.T) {
	ctx := context.Background()
	mock := disc.NewMock(0)

	// Add a client entry.
	clientPK, clientSK := cipher.GenerateKeyPair()
	serverPK, serverSK := cipher.GenerateKeyPair()

	clientEntry := disc.NewClientEntry(clientPK, 0, []cipher.PubKey{serverPK})
	require.NoError(t, clientEntry.Sign(clientSK))
	require.NoError(t, mock.PostEntry(ctx, clientEntry))

	// Add a server entry.
	serverEntry := disc.NewServerEntry(serverPK, 0, "server:8080", 10)
	require.NoError(t, serverEntry.Sign(serverSK))
	require.NoError(t, mock.PostEntry(ctx, serverEntry))

	servers, err := mock.AvailableServers(ctx)
	require.NoError(t, err)
	require.Len(t, servers, 1)
	assert.Equal(t, serverPK, servers[0].Static)
}

func TestMockAllServersReturnsServerEntries(t *testing.T) {
	ctx := context.Background()
	mock := disc.NewMock(0)

	pk1, sk1 := cipher.GenerateKeyPair()
	pk2, sk2 := cipher.GenerateKeyPair()

	e1 := disc.NewServerEntry(pk1, 0, "s1:1111", 5)
	require.NoError(t, e1.Sign(sk1))
	require.NoError(t, mock.PostEntry(ctx, e1))

	e2 := disc.NewServerEntry(pk2, 0, "s2:2222", 3)
	require.NoError(t, e2.Sign(sk2))
	require.NoError(t, mock.PostEntry(ctx, e2))

	servers, err := mock.AllServers(ctx)
	require.NoError(t, err)
	assert.Len(t, servers, 2)
}

// --- AllEntries ---

func TestMockAllEntries(t *testing.T) {
	ctx := context.Background()
	mock := disc.NewMock(0)

	pk1, sk1 := cipher.GenerateKeyPair()
	pk2, sk2 := cipher.GenerateKeyPair()

	e1 := disc.NewServerEntry(pk1, 0, "a:1", 1)
	require.NoError(t, e1.Sign(sk1))
	require.NoError(t, mock.PostEntry(ctx, e1))

	e2 := disc.NewClientEntry(pk2, 0, []cipher.PubKey{pk1})
	require.NoError(t, e2.Sign(sk2))
	require.NoError(t, mock.PostEntry(ctx, e2))

	entries, err := mock.AllEntries(ctx)
	require.NoError(t, err)
	assert.Len(t, entries, 2)

	hexes := map[string]bool{pk1.Hex(): true, pk2.Hex(): true}
	for _, h := range entries {
		assert.True(t, hexes[h], "unexpected hex key in AllEntries")
	}
}

// --- AllClientsByServer ---

func TestMockAllClientsByServer(t *testing.T) {
	ctx := context.Background()
	mock := disc.NewMock(0)

	serverPK, serverSK := cipher.GenerateKeyPair()
	serverEntry := disc.NewServerEntry(serverPK, 0, "srv:1000", 10)
	require.NoError(t, serverEntry.Sign(serverSK))
	require.NoError(t, mock.PostEntry(ctx, serverEntry))

	clientPK1, clientSK1 := cipher.GenerateKeyPair()
	clientPK2, clientSK2 := cipher.GenerateKeyPair()

	c1 := disc.NewClientEntry(clientPK1, 0, []cipher.PubKey{serverPK})
	require.NoError(t, c1.Sign(clientSK1))
	require.NoError(t, mock.PostEntry(ctx, c1))

	c2 := disc.NewClientEntry(clientPK2, 0, []cipher.PubKey{serverPK})
	require.NoError(t, c2.Sign(clientSK2))
	require.NoError(t, mock.PostEntry(ctx, c2))

	result, err := mock.AllClientsByServer(ctx)
	require.NoError(t, err)
	assert.Len(t, result[serverPK.Hex()], 2)
}

// --- ClientsByServer ---

func TestMockClientsByServer(t *testing.T) {
	ctx := context.Background()
	mock := disc.NewMock(0)

	serverPK1, serverSK1 := cipher.GenerateKeyPair()
	serverPK2, serverSK2 := cipher.GenerateKeyPair()

	s1 := disc.NewServerEntry(serverPK1, 0, "s1:1", 5)
	require.NoError(t, s1.Sign(serverSK1))
	require.NoError(t, mock.PostEntry(ctx, s1))

	s2 := disc.NewServerEntry(serverPK2, 0, "s2:2", 5)
	require.NoError(t, s2.Sign(serverSK2))
	require.NoError(t, mock.PostEntry(ctx, s2))

	clientPK, clientSK := cipher.GenerateKeyPair()
	c := disc.NewClientEntry(clientPK, 0, []cipher.PubKey{serverPK1})
	require.NoError(t, c.Sign(clientSK))
	require.NoError(t, mock.PostEntry(ctx, c))

	// Should find client under serverPK1.
	clients, err := mock.ClientsByServer(ctx, serverPK1)
	require.NoError(t, err)
	assert.Len(t, clients, 1)
	assert.Equal(t, clientPK, clients[0].Static)

	// Should find no clients under serverPK2.
	clients2, err := mock.ClientsByServer(ctx, serverPK2)
	require.NoError(t, err)
	assert.Empty(t, clients2)
}

// --- HTTPMessage String() ---

func TestHTTPMessageString(t *testing.T) {
	msg := disc.HTTPMessage{Code: http.StatusOK, Message: "wrote a new entry"}
	assert.Equal(t, "status code: 200. message: wrote a new entry", msg.String())

	msg2 := disc.HTTPMessage{Code: http.StatusNotFound, Message: "entry of public key is not found"}
	assert.Equal(t, "status code: 404. message: entry of public key is not found", msg2.String())

	msg3 := disc.HTTPMessage{Code: 0, Message: ""}
	assert.Equal(t, "status code: 0. message: ", msg3.String())
}

func TestExposedHTTPMessages(t *testing.T) {
	assert.Equal(t, http.StatusOK, disc.MsgEntrySet.Code)
	assert.Equal(t, "wrote a new entry", disc.MsgEntrySet.Message)

	assert.Equal(t, http.StatusOK, disc.MsgEntryUpdated.Code)
	assert.Equal(t, "wrote new entry iteration", disc.MsgEntryUpdated.Message)

	assert.Equal(t, http.StatusOK, disc.MsgEntryDeleted.Code)
	assert.Equal(t, "deleted entry", disc.MsgEntryDeleted.Message)
}

// --- NewHTTP constructor ---

func TestNewHTTPCreatesValidClient(t *testing.T) {
	log := logging.MustGetLogger("disc_test")
	client := disc.NewHTTP("http://localhost:9999", &http.Client{}, log)
	require.NotNil(t, client)

	// Verify it implements APIClient by attempting a call that will fail with connection refused.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	t.Cleanup(cancel)

	_, err := client.Entry(ctx, cipher.PubKey{})
	require.Error(t, err, "should fail because server is not running")
}

func TestNewHTTPWithNilHTTPClient(t *testing.T) {
	log := logging.MustGetLogger("disc_test")
	// Passing nil http.Client -- NewHTTP stores it; calls will panic/error at request time.
	client := disc.NewHTTP("http://localhost:9999", nil, log)
	require.NotNil(t, client)
}

// --- Entry Copy function ---

func TestCopyServerEntry(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	src := disc.NewServerEntry(pk, 5, "addr:8080", 20)
	require.NoError(t, src.Sign(sk))

	dst := &disc.Entry{}
	disc.Copy(dst, src)

	assert.Equal(t, src.Static, dst.Static)
	assert.Equal(t, src.Version, dst.Version)
	assert.Equal(t, src.Sequence, dst.Sequence)
	assert.Equal(t, src.Timestamp, dst.Timestamp)
	assert.Equal(t, src.Signature, dst.Signature)
	require.NotNil(t, dst.Server)
	assert.Equal(t, src.Server.Address, dst.Server.Address)
	assert.Equal(t, src.Server.AvailableSessions, dst.Server.AvailableSessions)
	assert.Nil(t, dst.Client)

	// Verify deep copy -- mutating dst should not affect src.
	dst.Server.Address = "changed"
	assert.NotEqual(t, src.Server.Address, dst.Server.Address)
}

func TestCopyClientEntry(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	serverPK, _ := cipher.GenerateKeyPair()
	src := disc.NewClientEntry(pk, 3, []cipher.PubKey{serverPK})
	require.NoError(t, src.Sign(sk))

	dst := &disc.Entry{}
	disc.Copy(dst, src)

	assert.Equal(t, src.Static, dst.Static)
	require.NotNil(t, dst.Client)
	require.Len(t, dst.Client.DelegatedServers, 1)
	assert.Equal(t, serverPK, dst.Client.DelegatedServers[0])
	assert.Nil(t, dst.Server)

	// Verify deep copy of DelegatedServers slice.
	dst.Client.DelegatedServers[0] = cipher.PubKey{}
	assert.NotEqual(t, src.Client.DelegatedServers[0], dst.Client.DelegatedServers[0])
}

func TestCopyNilFields(t *testing.T) {
	// Copy from empty to populated should clear dst fields.
	src := &disc.Entry{}
	dst := &disc.Entry{
		Client: &disc.Client{DelegatedServers: []cipher.PubKey{{}}},
		Server: &disc.Server{Address: "x"},
	}
	disc.Copy(dst, src)
	assert.Nil(t, dst.Client)
	assert.Nil(t, dst.Server)
}

// --- Error variables are distinct ---

func TestErrorVariablesAreDistinct(t *testing.T) {
	errs := []error{
		disc.ErrKeyNotFound,
		disc.ErrNoAvailableServers,
		disc.ErrUnexpected,
		disc.ErrUnauthorized,
		disc.ErrBadInput,
		disc.ErrValidationNonZeroSequence,
		disc.ErrValidationNilEphemerals,
		disc.ErrValidationNilKeys,
		disc.ErrValidationNonNilEphemerals,
		disc.ErrValidationNoSignature,
		disc.ErrValidationNoVersion,
		disc.ErrValidationNoClientOrServer,
		disc.ErrValidationWrongSequence,
		disc.ErrValidationWrongTime,
		disc.ErrValidationOutdatedTime,
		disc.ErrValidationServerAddress,
		disc.ErrValidationEmptyServerAddress,
		disc.ErrUnauthorizedNetworkMonitor,
	}

	// All errors must be non-nil and have distinct messages.
	seen := make(map[string]bool, len(errs))
	for _, e := range errs {
		require.NotNil(t, e)
		msg := e.Error()
		assert.False(t, seen[msg], "duplicate error message: %s", msg)
		seen[msg] = true
	}
}

// --- EntryValidationError ---

func TestEntryValidationErrorString(t *testing.T) {
	err := disc.NewEntryValidationError("test cause")
	assert.Equal(t, "entry validation error: test cause", err.Error())
}

// --- Entry and Server/Client String() methods ---

func TestEntryString(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	serverPK, _ := cipher.GenerateKeyPair()

	t.Run("server entry string", func(t *testing.T) {
		entry := disc.NewServerEntry(pk, 0, "addr:1234", 5)
		require.NoError(t, entry.Sign(sk))
		s := entry.String()
		assert.Contains(t, s, "registered as server")
		assert.Contains(t, s, "addr:1234")
	})

	t.Run("client entry string", func(t *testing.T) {
		entry := disc.NewClientEntry(pk, 0, []cipher.PubKey{serverPK})
		require.NoError(t, entry.Sign(sk))
		s := entry.String()
		assert.Contains(t, s, "registered as client")
		assert.Contains(t, s, "delegated servers")
	})
}

// --- NewClientEntry / NewServerEntry construction ---

func TestNewClientEntryFields(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	serverPK, _ := cipher.GenerateKeyPair()

	entry := disc.NewClientEntry(pk, 7, []cipher.PubKey{serverPK})
	assert.Equal(t, pk, entry.Static)
	assert.Equal(t, uint64(7), entry.Sequence)
	require.NotNil(t, entry.Client)
	assert.Len(t, entry.Client.DelegatedServers, 1)
	assert.Nil(t, entry.Server)
	assert.NotEmpty(t, entry.Version)
	assert.NotZero(t, entry.Timestamp)
}

func TestNewServerEntryFields(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	entry := disc.NewServerEntry(pk, 3, "myaddr:5555", 42)
	assert.Equal(t, pk, entry.Static)
	assert.Equal(t, uint64(3), entry.Sequence)
	require.NotNil(t, entry.Server)
	assert.Equal(t, "myaddr:5555", entry.Server.Address)
	assert.Equal(t, 42, entry.Server.AvailableSessions)
	assert.Nil(t, entry.Client)
	assert.NotEmpty(t, entry.Version)
	assert.NotZero(t, entry.Timestamp)
}

// --- Validate edge cases ---

func TestValidateNoVersion(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	entry := disc.NewServerEntry(pk, 0, "addr:1", 1)
	entry.Version = ""
	require.NoError(t, entry.Sign(sk))

	err := entry.Validate(false)
	assert.ErrorIs(t, err, disc.ErrValidationNoVersion)
}

func TestValidateNoSignature(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	entry := disc.NewServerEntry(pk, 0, "addr:1", 1)
	// Don't sign.
	err := entry.Validate(false)
	assert.ErrorIs(t, err, disc.ErrValidationNoSignature)
}

func TestValidateNoClientOrServer(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	entry := &disc.Entry{
		Version:   "0.0.1",
		Static:    pk,
		Timestamp: time.Now().UnixNano(),
	}
	require.NoError(t, entry.Sign(sk))
	err := entry.Validate(false)
	assert.ErrorIs(t, err, disc.ErrValidationNoClientOrServer)
}

func TestValidateEmptyServerAddress(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	entry := disc.NewServerEntry(pk, 0, "", 1)
	require.NoError(t, entry.Sign(sk))
	err := entry.Validate(false)
	assert.ErrorIs(t, err, disc.ErrValidationEmptyServerAddress)
}

func TestValidateTimestampOutdated(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	entry := disc.NewServerEntry(pk, 0, "addr:1", 1)
	entry.Timestamp = time.Now().Add(-10 * time.Minute).UnixNano()
	require.NoError(t, entry.Sign(sk))
	err := entry.Validate(true)
	assert.ErrorIs(t, err, disc.ErrValidationOutdatedTime)
}

func TestValidateTimestampFuture(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	entry := disc.NewServerEntry(pk, 0, "addr:1", 1)
	entry.Timestamp = time.Now().Add(10 * time.Minute).UnixNano()
	require.NoError(t, entry.Sign(sk))
	err := entry.Validate(true)
	assert.ErrorIs(t, err, disc.ErrValidationOutdatedTime)
}

func TestValidateTimestampSkipped(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	entry := disc.NewServerEntry(pk, 0, "addr:1", 1)
	// Old timestamp but validateTimestamp=false should pass.
	entry.Timestamp = time.Now().Add(-10 * time.Minute).UnixNano()
	require.NoError(t, entry.Sign(sk))
	err := entry.Validate(false)
	assert.NoError(t, err)
}

// --- Sign / VerifySignature ---

func TestSignAndVerify(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	entry := disc.NewServerEntry(pk, 0, "addr:1", 1)

	require.NoError(t, entry.Sign(sk))
	assert.NotEmpty(t, entry.Signature)
	assert.NoError(t, entry.VerifySignature())
}

func TestVerifySignatureWrongKey(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	_, sk2 := cipher.GenerateKeyPair()

	entry := disc.NewServerEntry(pk, 0, "addr:1", 1)
	require.NoError(t, entry.Sign(sk2)) // signed with wrong key
	err := entry.VerifySignature()
	assert.Error(t, err)

	_ = sk // use sk to avoid lint
}

func TestVerifySignatureTampered(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	entry := disc.NewServerEntry(pk, 0, "addr:1", 1)
	require.NoError(t, entry.Sign(sk))

	// Tamper with entry after signing.
	entry.Server.Address = "tampered"
	err := entry.VerifySignature()
	assert.Error(t, err)
}

// --- ValidateIteration ---

func TestValidateIterationSuccess(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	e1 := disc.NewServerEntry(pk, 0, "addr:1", 1)
	require.NoError(t, e1.Sign(sk))

	e2 := disc.NewServerEntry(pk, 1, "addr:1", 1)
	e2.Timestamp = e1.Timestamp + 1
	require.NoError(t, e2.Sign(sk))

	assert.NoError(t, e1.ValidateIteration(e2))
}

func TestValidateIterationWrongSeq(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	e1 := disc.NewServerEntry(pk, 5, "addr:1", 1)
	require.NoError(t, e1.Sign(sk))

	e2 := disc.NewServerEntry(pk, 3, "addr:1", 1)
	require.NoError(t, e2.Sign(sk))

	err := e1.ValidateIteration(e2)
	assert.ErrorIs(t, err, disc.ErrValidationWrongSequence)
}

func TestValidateIterationWrongTimestamp(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	e1 := disc.NewServerEntry(pk, 0, "addr:1", 1)
	require.NoError(t, e1.Sign(sk))

	e2 := disc.NewServerEntry(pk, 1, "addr:1", 1)
	e2.Timestamp = e1.Timestamp - 1000
	require.NoError(t, e2.Sign(sk))

	err := e1.ValidateIteration(e2)
	assert.ErrorIs(t, err, disc.ErrValidationWrongTime)
}

// --- Mock with timeout ---

func TestMockClientWithTimeout(t *testing.T) {
	ctx := context.Background()
	mock := disc.NewMock(200 * time.Millisecond)

	pk, sk := cipher.GenerateKeyPair()
	entry := disc.NewServerEntry(pk, 0, "addr:1", 1)
	require.NoError(t, entry.Sign(sk))
	require.NoError(t, mock.PostEntry(ctx, entry))

	// Entry should exist immediately.
	_, err := mock.Entry(ctx, pk)
	require.NoError(t, err)

	// After timeout, entry should be removed.
	time.Sleep(400 * time.Millisecond)
	_, err = mock.Entry(ctx, pk)
	require.Error(t, err, "entry should have been removed after timeout")
}

// --- NilKeys validation ---

func TestValidateNilKeys(t *testing.T) {
	entry := &disc.Entry{
		Version:   "0.0.1",
		Signature: "something",
		Client:    &disc.Client{},
	}
	err := entry.Validate(false)
	assert.ErrorIs(t, err, disc.ErrValidationNilKeys)
}

// ===========================================================================
// httpClient tests using httptest
// ===========================================================================

// helper: create a test server + httpClient pair
func newTestHTTPClient(t *testing.T, handler http.Handler) disc.APIClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	log := logging.MustGetLogger("disc_http_test")
	return disc.NewHTTP(srv.URL, srv.Client(), log)
}

func TestHTTPClientEntry_Success(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	entry := disc.NewServerEntry(pk, 0, "addr:9090", 5)
	require.NoError(t, entry.Sign(sk))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Contains(t, r.URL.Path, "/dmsg-discovery/entry/")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entry) //nolint:errcheck,gosec
	})

	client := newTestHTTPClient(t, handler)
	got, err := client.Entry(context.Background(), pk)
	require.NoError(t, err)
	assert.Equal(t, pk, got.Static)
	assert.Equal(t, "addr:9090", got.Server.Address)
}

func TestHTTPClientEntry_NotFound(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(disc.HTTPMessage{ //nolint:errcheck,gosec
			Code:    http.StatusNotFound,
			Message: disc.ErrKeyNotFound.Error(),
		})
	})

	client := newTestHTTPClient(t, handler)
	_, err := client.Entry(context.Background(), cipher.PubKey{})
	require.Error(t, err)
	assert.Equal(t, disc.ErrKeyNotFound, err)
}

func TestHTTPClientEntry_UnknownErrorBecomesUnexpected(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(disc.HTTPMessage{ //nolint:errcheck,gosec
			Code:    http.StatusInternalServerError,
			Message: "some unknown error",
		})
	})

	client := newTestHTTPClient(t, handler)
	_, err := client.Entry(context.Background(), cipher.PubKey{})
	require.Error(t, err)
	assert.Equal(t, disc.ErrUnexpected, err)
}

func TestHTTPClientPostEntry_Success(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/dmsg-discovery/entry/", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	})

	pk, sk := cipher.GenerateKeyPair()
	entry := disc.NewServerEntry(pk, 0, "addr:1", 1)
	require.NoError(t, entry.Sign(sk))

	client := newTestHTTPClient(t, handler)
	err := client.PostEntry(context.Background(), entry)
	require.NoError(t, err)
}

func TestHTTPClientPostEntry_Error(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		body, _ := json.Marshal(disc.HTTPMessage{ //nolint:errcheck,gosec
			Code:    http.StatusUnprocessableEntity,
			Message: disc.ErrValidationWrongSequence.Error(),
		})
		w.Write(body) //nolint:errcheck,gosec
	})

	pk, sk := cipher.GenerateKeyPair()
	entry := disc.NewServerEntry(pk, 0, "addr:1", 1)
	require.NoError(t, entry.Sign(sk))

	client := newTestHTTPClient(t, handler)
	err := client.PostEntry(context.Background(), entry)
	require.Error(t, err)
	assert.Equal(t, disc.ErrValidationWrongSequence, err)
}

func TestHTTPClientDelEntry_Success(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		assert.Equal(t, "/dmsg-discovery/entry", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	})

	pk, sk := cipher.GenerateKeyPair()
	entry := disc.NewServerEntry(pk, 0, "addr:1", 1)
	require.NoError(t, entry.Sign(sk))

	client := newTestHTTPClient(t, handler)
	err := client.DelEntry(context.Background(), entry)
	require.NoError(t, err)
}

func TestHTTPClientDelEntry_Error(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		body, _ := json.Marshal(disc.HTTPMessage{ //nolint:errcheck,gosec
			Code:    http.StatusUnauthorized,
			Message: disc.ErrUnauthorized.Error(),
		})
		w.Write(body) //nolint:errcheck,gosec
	})

	pk, sk := cipher.GenerateKeyPair()
	entry := disc.NewServerEntry(pk, 0, "addr:1", 1)
	require.NoError(t, entry.Sign(sk))

	client := newTestHTTPClient(t, handler)
	err := client.DelEntry(context.Background(), entry)
	require.Error(t, err)
	assert.Equal(t, disc.ErrUnauthorized, err)
}

func TestHTTPClientAvailableServers_Success(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	entry := disc.NewServerEntry(pk, 0, "addr:8080", 10)
	require.NoError(t, entry.Sign(sk))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/dmsg-discovery/available_servers", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]*disc.Entry{entry}) //nolint:errcheck,gosec
	})

	client := newTestHTTPClient(t, handler)
	servers, err := client.AvailableServers(context.Background())
	require.NoError(t, err)
	require.Len(t, servers, 1)
	assert.Equal(t, pk, servers[0].Static)
}

func TestHTTPClientAvailableServers_Error(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(disc.HTTPMessage{ //nolint:errcheck,gosec
			Code:    http.StatusInternalServerError,
			Message: disc.ErrNoAvailableServers.Error(),
		})
	})

	client := newTestHTTPClient(t, handler)
	_, err := client.AvailableServers(context.Background())
	require.Error(t, err)
	assert.Equal(t, disc.ErrNoAvailableServers, err)
}

func TestHTTPClientAllServers_Success(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	entry := disc.NewServerEntry(pk, 0, "addr:8080", 10)
	require.NoError(t, entry.Sign(sk))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/dmsg-discovery/all_servers", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]*disc.Entry{entry}) //nolint:errcheck,gosec
	})

	client := newTestHTTPClient(t, handler)
	servers, err := client.AllServers(context.Background())
	require.NoError(t, err)
	require.Len(t, servers, 1)
}

func TestHTTPClientAllServers_Error(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(disc.HTTPMessage{ //nolint:errcheck,gosec
			Code:    http.StatusInternalServerError,
			Message: disc.ErrUnexpected.Error(),
		})
	})

	client := newTestHTTPClient(t, handler)
	_, err := client.AllServers(context.Background())
	require.Error(t, err)
	assert.Equal(t, disc.ErrUnexpected, err)
}

func TestHTTPClientAllEntries_Success(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/dmsg-discovery/entries", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]string{"abc123", "def456"}) //nolint:errcheck,gosec
	})

	client := newTestHTTPClient(t, handler)
	entries, err := client.AllEntries(context.Background())
	require.NoError(t, err)
	assert.Len(t, entries, 2)
}

func TestHTTPClientAllEntries_Error(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(disc.HTTPMessage{ //nolint:errcheck,gosec
			Code:    http.StatusInternalServerError,
			Message: disc.ErrBadInput.Error(),
		})
	})

	client := newTestHTTPClient(t, handler)
	_, err := client.AllEntries(context.Background())
	require.Error(t, err)
	assert.Equal(t, disc.ErrBadInput, err)
}

func TestHTTPClientAllClientsByServer_Success(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	serverPK, _ := cipher.GenerateKeyPair()
	entry := disc.NewClientEntry(pk, 0, []cipher.PubKey{serverPK})
	require.NoError(t, entry.Sign(sk))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/dmsg-discovery/servers/clients", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		result := map[string][]*disc.Entry{
			serverPK.Hex(): {entry},
		}
		json.NewEncoder(w).Encode(result) //nolint:errcheck,gosec
	})

	client := newTestHTTPClient(t, handler)
	result, err := client.AllClientsByServer(context.Background())
	require.NoError(t, err)
	assert.Len(t, result[serverPK.Hex()], 1)
}

func TestHTTPClientAllClientsByServer_Error(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(disc.HTTPMessage{ //nolint:errcheck,gosec
			Code:    http.StatusInternalServerError,
			Message: disc.ErrUnexpected.Error(),
		})
	})

	client := newTestHTTPClient(t, handler)
	_, err := client.AllClientsByServer(context.Background())
	require.Error(t, err)
	assert.Equal(t, disc.ErrUnexpected, err)
}

func TestHTTPClientClientsByServer_Success(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	serverPK, _ := cipher.GenerateKeyPair()
	entry := disc.NewClientEntry(pk, 0, []cipher.PubKey{serverPK})
	require.NoError(t, entry.Sign(sk))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expectedPath := fmt.Sprintf("/dmsg-discovery/server/%s/clients", serverPK.Hex())
		assert.Equal(t, expectedPath, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]*disc.Entry{entry}) //nolint:errcheck,gosec
	})

	client := newTestHTTPClient(t, handler)
	entries, err := client.ClientsByServer(context.Background(), serverPK)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, pk, entries[0].Static)
}

func TestHTTPClientClientsByServer_Error(t *testing.T) {
	serverPK, _ := cipher.GenerateKeyPair()

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(disc.HTTPMessage{ //nolint:errcheck,gosec
			Code:    http.StatusNotFound,
			Message: disc.ErrKeyNotFound.Error(),
		})
	})

	client := newTestHTTPClient(t, handler)
	_, err := client.ClientsByServer(context.Background(), serverPK)
	require.Error(t, err)
	assert.Equal(t, disc.ErrKeyNotFound, err)
}

func TestHTTPClientPutEntry_Success(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	entry := disc.NewServerEntry(pk, 0, "addr:1", 1)
	require.NoError(t, entry.Sign(sk))

	// PutEntry calls PostEntry internally. The server just returns OK.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusOK)
			return
		}
		// GET for Entry lookup (shouldn't be needed if PostEntry succeeds)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entry) //nolint:errcheck,gosec
	})

	client := newTestHTTPClient(t, handler)
	err := client.PutEntry(context.Background(), sk, entry)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), entry.Sequence)
}

func TestHTTPClientEntry_ConnectionRefused(t *testing.T) {
	log := logging.MustGetLogger("disc_http_test")
	// Use a URL that will fail to connect.
	client := disc.NewHTTP("http://127.0.0.1:1", &http.Client{Timeout: 100 * time.Millisecond}, log)
	_, err := client.Entry(context.Background(), cipher.PubKey{})
	require.Error(t, err)
}

func TestHTTPClientPostEntry_ConnectionRefused(t *testing.T) {
	log := logging.MustGetLogger("disc_http_test")
	client := disc.NewHTTP("http://127.0.0.1:1", &http.Client{Timeout: 100 * time.Millisecond}, log)
	pk, sk := cipher.GenerateKeyPair()
	entry := disc.NewServerEntry(pk, 0, "addr:1", 1)
	require.NoError(t, entry.Sign(sk))
	err := client.PostEntry(context.Background(), entry)
	require.Error(t, err)
}

func TestHTTPClientDelEntry_ConnectionRefused(t *testing.T) {
	log := logging.MustGetLogger("disc_http_test")
	client := disc.NewHTTP("http://127.0.0.1:1", &http.Client{Timeout: 100 * time.Millisecond}, log)
	pk, sk := cipher.GenerateKeyPair()
	entry := disc.NewServerEntry(pk, 0, "addr:1", 1)
	require.NoError(t, entry.Sign(sk))
	err := client.DelEntry(context.Background(), entry)
	require.Error(t, err)
}

func TestHTTPClientAvailableServers_ConnectionRefused(t *testing.T) {
	log := logging.MustGetLogger("disc_http_test")
	client := disc.NewHTTP("http://127.0.0.1:1", &http.Client{Timeout: 100 * time.Millisecond}, log)
	_, err := client.AvailableServers(context.Background())
	require.Error(t, err)
}

func TestHTTPClientAllServers_ConnectionRefused(t *testing.T) {
	log := logging.MustGetLogger("disc_http_test")
	client := disc.NewHTTP("http://127.0.0.1:1", &http.Client{Timeout: 100 * time.Millisecond}, log)
	_, err := client.AllServers(context.Background())
	require.Error(t, err)
}
