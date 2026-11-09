package alerts

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func kinds(evs []Event) []string {
	out := []string{}
	for _, e := range evs {
		out = append(out, e.Kind)
	}
	return out
}

func TestTrackerOfflineAfterDelayOnce(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	tr := NewTracker(nil)
	t0 := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	delay := 5 * time.Minute
	on := []Target{{PK: pk, Online: true, Hostname: "site-a"}}
	off := []Target{{PK: pk}}

	require.Empty(t, tr.Observe(t0, delay, on))
	require.Empty(t, tr.Observe(t0.Add(time.Minute), delay, off), "not yet past the delay")
	require.Empty(t, tr.Observe(t0.Add(5*time.Minute), delay, off))
	evs := tr.Observe(t0.Add(6*time.Minute), delay, off)
	require.Equal(t, []string{KindOffline}, kinds(evs))
	require.Equal(t, "site-a", evs[0].Target.Hostname, "details from earlier checks are kept")
	require.Equal(t, t0.Add(time.Minute), evs[0].Since)

	for i := 7; i < 60; i++ {
		require.Empty(t, tr.Observe(t0.Add(time.Duration(i)*time.Minute), delay, off), "no repeat while still offline")
	}
	evs = tr.Observe(t0.Add(61*time.Minute), delay, on)
	require.Equal(t, []string{KindOnline}, kinds(evs))
	require.Equal(t, t0.Add(time.Minute), evs[0].Since)
	require.Empty(t, tr.Observe(t0.Add(62*time.Minute), delay, on), "no repeat while online")
}

func TestTrackerBackWithinDelaySendsNothing(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	tr := NewTracker(nil)
	t0 := time.Now()
	delay := time.Minute
	on := []Target{{PK: pk, Online: true}}
	off := []Target{{PK: pk}}
	// Flapping every 30s never reaches the 1m delay.
	for i := 0; i < 100; i++ {
		ts := t0.Add(time.Duration(i) * 30 * time.Second)
		set := on
		if i%2 == 1 {
			set = off
		}
		require.Empty(t, tr.Observe(ts, delay, set), "step %d", i)
	}
}

func TestTrackerUsesLastSeenAndFloor(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	t0 := time.Now()
	delay := 5 * time.Minute
	off := []Target{{PK: pk, LastSeen: t0.Add(-time.Hour)}}

	tr := NewTracker(nil)
	evs := tr.Observe(t0, delay, off)
	require.Equal(t, []string{KindOffline}, kinds(evs), "offline an hour already")
	require.Equal(t, t0.Add(-time.Hour), evs[0].Since)

	tr = NewTracker(nil)
	tr.Floor = t0
	require.Empty(t, tr.Observe(t0, delay, off), "the floor gives a full delay after start")
	require.Empty(t, tr.Observe(t0.Add(4*time.Minute), delay, off))
	evs = tr.Observe(t0.Add(5*time.Minute), delay, off)
	require.Equal(t, []string{KindOffline}, kinds(evs))
	require.Equal(t, t0.Add(-time.Hour), evs[0].Since, "the mail still says when it was last seen")
}

func TestTrackerRestoresAlerted(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	since := time.Now().Add(-time.Hour)
	tr := NewTracker(map[cipher.PubKey]time.Time{pk: since})
	require.Empty(t, tr.Observe(time.Now(), time.Minute, []Target{{PK: pk}}), "no repeat after a restart")
	evs := tr.Observe(time.Now(), time.Minute, []Target{{PK: pk, Online: true}})
	require.Equal(t, []string{KindOnline}, kinds(evs), "recovery still sent after a restart")
	require.Empty(t, tr.Alerted())
}

func TestTrackerDropsUnwatched(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	tr := NewTracker(nil)
	tr.Observe(time.Now(), time.Minute, []Target{{PK: pk}})
	tr.Observe(time.Now(), time.Minute, nil)
	require.Empty(t, tr.States())
}

func TestSettingsValidate(t *testing.T) {
	ok := Settings{EmailTo: []string{"ops@example.com"}, OfflineAfter: Duration(time.Minute), Webhook: "https://example.com/h"}
	require.NoError(t, ok.Validate())
	for _, d := range []time.Duration{59 * time.Second, time.Hour + time.Second} {
		bad := ok
		bad.OfflineAfter = Duration(d)
		require.Error(t, bad.Validate(), d.String())
	}
	bad := ok
	bad.OfflineAfter = Duration(time.Hour)
	require.NoError(t, bad.Validate())
	bad.EmailTo = []string{"nope"}
	require.Error(t, bad.Validate())
	bad = ok
	bad.Webhook = "ftp://x"
	require.Error(t, bad.Validate())
	require.Equal(t, DefaultOfflineAfter, (&Settings{}).Delay())
}

func TestRedactAndMerge(t *testing.T) {
	cur := &Settings{SMTP: &SMTP{Host: "smtp.example.com", Username: "u", Password: "secret"}}
	r := cur.Redacted()
	require.Empty(t, r.SMTP.Password)
	require.True(t, r.SMTP.PasswordSet)
	require.Equal(t, "secret", cur.SMTP.Password, "the original is untouched")

	next := Merge(cur, Settings{SMTP: &SMTP{Host: "smtp.example.com", Username: "u"}})
	require.Equal(t, "secret", next.SMTP.Password, "an empty password keeps the current one")
	next = Merge(cur, Settings{SMTP: &SMTP{Host: "other.example.com"}})
	require.Empty(t, next.SMTP.Password, "a different relay does not inherit it")
}
