package commands

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skymail"
)

func TestLimitsFromFlags(t *testing.T) {
	l, err := options{maxMessage: "4096", maxTotal: "none", maxAge: "1h0m0s"}.limits()
	require.NoError(t, err)
	require.Equal(t, skymail.Limits{MaxMessageSize: 4096, MaxTotalSize: -1, MaxAge: time.Hour}, l)

	l, err = options{}.limits()
	require.NoError(t, err)
	require.Equal(t, skymail.Limits{}, l, "no flags: the defaults")

	_, err = options{maxAge: "soon"}.limits()
	require.Error(t, err)
}

func TestMailSender(t *testing.T) {
	require.Equal(t, "Alice", mailSender(`"Alice" <a@b.skynet>`))
	require.Equal(t, "a@b.skynet", mailSender("a@b.skynet"))
	require.Equal(t, "bob@abcdefgh….skynet", mailSender("bob@abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrs.skynet"))
}
