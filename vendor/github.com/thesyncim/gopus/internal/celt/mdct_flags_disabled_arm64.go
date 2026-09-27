//go:build arm64

package celt

// Match the pinned libopus arm64 float-path accumulation pattern on long-frame
// CELT MDCT mixes. This closes real packet-decision drift on parity fixtures.
const mdctUseFMALikeMixEnabled = true
