package dmsgserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg/metrics"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgserver"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

func TestNew_HealthEndpoint(t *testing.T) {
	r := chi.NewRouter()
	log := logging.MustGetLogger("test")
	m := metrics.NewEmpty()

	a := dmsgserver.NewServerAPI(r, log, m)
	require.NotNil(t, a)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}

func TestSetDmsgServer(t *testing.T) {
	r := chi.NewRouter()
	log := logging.MustGetLogger("test")
	m := metrics.NewEmpty()

	a := dmsgserver.NewServerAPI(r, log, m)
	require.NotNil(t, a)

	// SetDmsgServer should not panic with a nil server
	assert.NotPanics(t, func() {
		a.SetDmsgServer((*dmsg.Server)(nil))
	})
}

func TestHealthEndpoint_ResponseFields(t *testing.T) {
	r := chi.NewRouter()
	log := logging.MustGetLogger("test")
	m := metrics.NewEmpty()

	a := dmsgserver.NewServerAPI(r, log, m)
	require.NotNil(t, a)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var body map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Contains(t, body, "build_info")
	assert.Contains(t, body, "started_at")
}

func TestRunBackgroundTasks_Cancellation(t *testing.T) {
	r := chi.NewRouter()
	log := logging.MustGetLogger("test")
	m := metrics.NewEmpty()

	a := dmsgserver.NewServerAPI(r, log, m)
	require.NotNil(t, a)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		a.RunBackgroundTasks(ctx)
		close(done)
	}()

	// Cancel should cause RunBackgroundTasks to return
	cancel()
	select {
	case <-done:
		// success
	case <-time.After(5 * time.Second):
		t.Fatal("RunBackgroundTasks did not exit after context cancellation")
	}
}

func TestRunBackgroundTasks_WithNilServer(t *testing.T) {
	r := chi.NewRouter()
	log := logging.MustGetLogger("test")
	m := metrics.NewEmpty()

	a := dmsgserver.NewServerAPI(r, log, m)
	require.NotNil(t, a)

	// Should not panic with nil dmsg server
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	assert.NotPanics(t, func() {
		a.RunBackgroundTasks(ctx)
	})
}
