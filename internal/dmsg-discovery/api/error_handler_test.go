// Package api internal/dmsg-discovery/api/error_handler_test.go
package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/internal/discmetrics"
	"github.com/skycoin/skywire/internal/dmsg-discovery/store"
	"github.com/skycoin/skywire/pkg/disc"
)

var errHandlerTestCases = []struct {
	err        error
	statusCode int
	message    string
}{
	{disc.ErrKeyNotFound, http.StatusNotFound, "entry of public key is not found"},
	{disc.ErrUnexpected, http.StatusInternalServerError, "something unexpected happened"},
	{disc.ErrUnauthorized, http.StatusUnauthorized, "invalid signature"},
	{disc.ErrBadInput, http.StatusBadRequest, "error bad input"},
	{
		disc.NewEntryValidationError("entry Keys is nil"),
		http.StatusUnprocessableEntity,
		"entry validation error: entry Keys is nil",
	},
}

func TestErrorHandler(t *testing.T) {
	for _, tc := range errHandlerTestCases {
		tc := tc
		t.Run(tc.err.Error(), func(t *testing.T) {
			w := httptest.NewRecorder()
			api := New(nil, store.NewMock(), discmetrics.NewEmpty(), true, false, true, "")
			api.handleError(w, &http.Request{}, tc.err)

			msg := new(disc.HTTPMessage)
			err := json.NewDecoder(w.Body).Decode(&msg)
			require.NoError(t, err)

			assert.Equal(t, tc.statusCode, msg.Code)
			assert.Equal(t, tc.message, msg.Message)
		})
	}
}
