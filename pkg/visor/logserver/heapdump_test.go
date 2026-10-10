package logserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHeapDumpWritesFile(t *testing.T) {
	dir := t.TempDir()
	rr := httptest.NewRecorder()
	heapDumpHandler(dir)(rr, httptest.NewRequest(http.MethodPost, "/debug/heapdump", nil))
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	path := strings.Fields(rr.Body.String())[0]
	st, err := os.Stat(path)
	require.NoError(t, err)
	require.Positive(t, st.Size())

	rr = httptest.NewRecorder()
	heapDumpHandler("")(rr, httptest.NewRequest(http.MethodPost, "/debug/heapdump", nil))
	require.Equal(t, http.StatusNotFound, rr.Code)
}
