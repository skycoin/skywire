//go:build arm64

package celt

const mdctUseNativeMulEnabled = false

// Match the pinned libopus arm64 float-path accumulation pattern on long-frame
// CELT MDCT mixes. This closes real packet-decision drift on parity fixtures.
const mdctUseFMALikeMixEnabled = true

// 5 ms forward MDCT matches the same fused-like mix path as the other CELT sizes.
const mdctUseNativeMulShort240Enabled = false
