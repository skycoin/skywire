package vpn

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHandshakeStatusString(t *testing.T) {
	cases := map[HandshakeStatus]string{
		HandshakeStatusOK:            "OK",
		HandshakeStatusBadRequest:    "Request was malformed",
		HandshakeNoFreeIPs:           "No free IPs left to serve",
		HandshakeStatusInternalError: "Internal server error",
		HandshakeStatusForbidden:     "Forbidden",
		HandshakeStatus(99):          "Unknown code",
	}
	for hs, want := range cases {
		require.Equal(t, want, hs.String())
	}
}

func TestHandshakeStatusGetError(t *testing.T) {
	require.NoError(t, HandshakeStatusOK.getError())

	require.ErrorIs(t, HandshakeStatusBadRequest.getError(), errHandshakeStatusBadRequest)
	require.ErrorIs(t, HandshakeNoFreeIPs.getError(), errHandshakeNoFreeIPs)
	require.ErrorIs(t, HandshakeStatusInternalError.getError(), errHandshakeStatusInternalError)
	require.ErrorIs(t, HandshakeStatusForbidden.getError(), errHandshakeStatusForbidden)

	// Unknown code → a generic non-nil error.
	require.Error(t, HandshakeStatus(99).getError())
}
