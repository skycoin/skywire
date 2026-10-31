package climail

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skymail"
)

func TestRenderSendSeparatesQueuedFromFailed(t *testing.T) {
	out := renderSend(&skymail.SendResult{
		OutboxID: "1.Mab.skymail",
		Recipients: []skymail.RcptResult{
			{Rcpt: "a@x.dmsg", Via: "dmsg"},
			{Rcpt: "b@x.dmsg", Err: "dial: offline", Queued: true},
			{Rcpt: "c@x.dmsg", Err: "550 not whitelisted"},
		},
	})
	require.Contains(t, out, "delivered  a@x.dmsg (via dmsg)")
	require.Contains(t, out, "queued     b@x.dmsg: dial: offline")
	require.Contains(t, out, "FAILED     c@x.dmsg: 550 not whitelisted")
	require.Contains(t, out, "queued as 1.Mab.skymail")
}

func TestRenderOutbox(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	out := renderOutbox([]skymail.Summary{{
		ID: "1.Mab.skymail", Subject: "hi",
		Queue: &skymail.Queued{
			Pending: []string{"b@x.dmsg"}, Attempts: 3, LastError: "dial: offline",
			NextAttempt: now.Add(90 * time.Second),
		},
	}}, now)
	require.Contains(t, out, "b@x.dmsg")
	require.Contains(t, out, "in 1m30s")
	require.Contains(t, out, "never", "no give-up time")
	require.Contains(t, out, "0 helpers, 0 servers")
	require.Contains(t, out, "dial: offline")
	require.Equal(t, "(empty)\n", renderOutbox(nil, now))

	out = renderOutbox([]skymail.Summary{{
		ID:    "2.Mcd.skymail",
		Queue: &skymail.Queued{Pending: []string{"b@x.dmsg"}, CarriedFor: "02aa", NextAttempt: now},
	}}, now)
	require.Contains(t, out, "(sealed)")
	require.Contains(t, out, "carrying for 02aa")
}

func TestRenderMessageNamesTheRelay(t *testing.T) {
	out := renderMessage(&skymail.Rendered{From: "a@x.dmsg", PeerPK: "02aa", RelayPK: "03bb", Verified: true})
	require.Contains(t, out, "(verified: signed by 02aa, relayed by 03bb)")
	out = renderMessage(&skymail.Rendered{From: "a@x.dmsg", PeerPK: "02aa", Verified: true})
	require.Contains(t, out, "(verified: delivered by 02aa)")
}
