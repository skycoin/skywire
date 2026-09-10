// Package api pkg/dmsg/discovery/api/error_level_test.go c1-net-dmsg
package api

import (
	"io"
	"net/http"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

type levelHook struct{ entries []*logrus.Entry }

func (h *levelHook) Levels() []logrus.Level { return logrus.AllLevels }
func (h *levelHook) Fire(e *logrus.Entry) error {
	h.entries = append(h.entries, e)
	return nil
}

// Rejecting a peer's bad request is not a warning about this service. A remote
// peer re-posting a stale entry could otherwise fill the discovery's log at
// Warn — the 422 below was measured at ~88 per two minutes in production.
func TestLogAPIErrorLevelByStatus(t *testing.T) {
	l := logrus.New()
	l.SetOutput(io.Discard)
	l.SetLevel(logrus.DebugLevel)
	h := &levelHook{}
	l.AddHook(h)

	// A lookup for an unregistered key is the ordinary answer: unlogged.
	logAPIError(l, http.StatusNotFound, disc.ErrKeyNotFound)
	require.Empty(t, h.entries)

	// 4xx is the peer's mistake, and the response already says so.
	logAPIError(l, http.StatusUnprocessableEntity,
		disc.NewEntryValidationError("sequence field of new entry is not sequence of old entry"))
	require.Len(t, h.entries, 1)
	require.Equal(t, logrus.DebugLevel, h.entries[0].Level)

	logAPIError(l, http.StatusUnauthorized, disc.ErrUnauthorized)
	require.Len(t, h.entries, 2)
	require.Equal(t, logrus.DebugLevel, h.entries[1].Level)

	// 5xx is this service failing, and stays a warning.
	logAPIError(l, http.StatusInternalServerError, disc.ErrUnexpected)
	require.Len(t, h.entries, 3)
	require.Equal(t, logrus.WarnLevel, h.entries[2].Level)
	require.Contains(t, h.entries[2].Message, "500")
}
