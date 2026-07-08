//go:build gopus_dred || gopus_osce

package extsupport

// DNNBlob reports whether USE_WEIGHTS_FILE model-blob loading is compiled in.
//
// libopus keeps every DNN/model loader behind compile flags
// (ENABLE_DRED/ENABLE_OSCE/ENABLE_DEEP_PLC); the loaders only make sense when a
// model-CONSUMING runtime is compiled in. This mirrors that gating by sharing
// the same tags as the DRED/OSCE runtime hooks.
const DNNBlob = true
