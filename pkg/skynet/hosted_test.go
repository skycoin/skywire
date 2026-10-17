package skynet

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestHostedAttestation(t *testing.T) {
	svc, svcSK := cipher.GenerateKeyPair()
	host, _ := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()
	sig, err := SignHosted(svc, svcSK, host)
	require.NoError(t, err)
	require.NoError(t, VerifyHosted(svc, host, sig))
	require.Error(t, VerifyHosted(svc, other, sig), "the proof names one host")
	require.Error(t, VerifyHosted(other, host, sig), "and one service")
}
