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
	require.Contains(t, out, "dial: offline")
	require.Equal(t, "(empty)\n", renderOutbox(nil, now))
}
