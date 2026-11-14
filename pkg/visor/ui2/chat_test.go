package ui2

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChatOf(t *testing.T) {
	ms, err := ParseChatHistory([]byte(`[
		{"peer":"b","outgoing":true,"text":"two","timestamp":"2026-11-11T10:00:02Z","id":"2"},
		{"peer":"a","outgoing":false,"message":"one","timestamp":"2026-11-11T10:00:01Z","id":"1"},
		{"peer":"a","outgoing":true,"text":"re","timestamp":"2026-11-11T10:00:03Z","id":"3","reply_to":"1"}]`))
	require.NoError(t, err)
	c := ChatOf(Chat{Peer: "a"}, ms)
	require.Equal(t, []string{"a", "b"}, c.Peers, "newest conversation first")
	require.Len(t, c.Msgs, 2)
	require.Equal(t, "one", c.Msgs[0].Body())
	require.Equal(t, "one", c.QuoteOf("1"))
	c = ChatOf(Chat{Peer: "new"}, ms)
	require.Equal(t, "new", c.Peers[0], "a peer picked before any message is listed")
}
