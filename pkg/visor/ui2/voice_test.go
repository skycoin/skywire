package ui2

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseVoiceRing(t *testing.T) {
	require.Equal(t, VoiceRing{ID: "abc", From: "02ff"}, ParseVoiceRing("abc from 02ff"))
	require.Equal(t, VoiceRing{ID: "abc"}, ParseVoiceRing("abc"))
	require.Equal(t, "0:07", voiceClock(7))
	require.Equal(t, "12:34", voiceClock(754))
}
