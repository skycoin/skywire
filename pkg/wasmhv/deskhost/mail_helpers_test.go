package deskhost

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skymail"
)

func TestReplyTo(t *testing.T) {
	r := replyTo(&skymail.Rendered{
		From: "Bob <bob@x.skynet>", Subject: "hi", Date: "Mon", MessageID: "<m@x>", Text: "one\r\ntwo\r\n",
		To: "carol@elsewhere.skynet, Alice <alice@host.MINE.skynet>",
	}, "mail@mine.skynet")
	require.Equal(t, []string{"bob@x.skynet"}, r.To)
	require.Equal(t, "Re: hi", r.Subject)
	require.Equal(t, "<m@x>", r.InReplyTo)
	require.Equal(t, "alice@host.MINE.skynet", r.From, "reply from the address it was sent to")
	require.Equal(t, "\n\nMon, Bob <bob@x.skynet> wrote:\n> one\n> two\n", r.Body)
	require.Equal(t, "RE: hi", replyTo(&skymail.Rendered{Subject: "RE: hi"}, "").Subject)
}

func TestAddressedToFallsBackToDefault(t *testing.T) {
	m := &skymail.Rendered{To: "someone@other.skynet", Cc: "x@mine.dmsg"}
	require.Equal(t, "x@mine.dmsg", addressedTo(m, "mail@mine.skynet"), "Cc counts, and .dmsg is the same PK")
	require.Equal(t, "", addressedTo(&skymail.Rendered{To: "a@other.skynet"}, "mail@mine.skynet"), "not ours: default From")
	require.Equal(t, "", addressedTo(m, ""))
}

func TestSplitAddrs(t *testing.T) {
	require.Equal(t, []string{"a@x", "b@y", "c@z"}, splitAddrs(" a@x, b@y;\nc@z "))
	require.Empty(t, splitAddrs("  "))
}

func TestWhitelistEdits(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	wl := withPK(nil, a)
	wl = withPK(wl, a)
	require.Equal(t, []cipher.PubKey{a}, wl, "no duplicates")
	wl = withPK(wl, b)
	require.Equal(t, []cipher.PubKey{b}, withoutPK(wl, a))
	require.Empty(t, withoutPK([]cipher.PubKey{a}, a))
}

func TestSettingsUpdate(t *testing.T) {
	u, err := settingsUpdate(true, "2MiB", "none", "3d")
	require.NoError(t, err)
	require.True(t, *u.Enable)
	require.Equal(t, int64(2<<20), *u.MaxMessageSize)
	require.Equal(t, int64(-1), *u.MaxTotalSize)
	require.Equal(t, 72*time.Hour, *u.MaxAge)
	_, err = settingsUpdate(true, "big", "1MiB", "1d")
	require.Error(t, err)
}

func TestDelivered(t *testing.T) {
	ok, via, failed := delivered(&skymail.SendResult{Recipients: []skymail.RcptResult{
		{Rcpt: "a", Via: "dmsg"}, {Rcpt: "b", Err: "offline"},
	}})
	require.Equal(t, []string{"a"}, ok)
	require.Equal(t, []string{"dmsg"}, via)
	require.Equal(t, []string{"b: offline"}, failed)
}
