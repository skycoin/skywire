//go:build !gopus_dred && !gopus_osce

package extsupport

// DNNBlob reports whether USE_WEIGHTS_FILE model-blob loading is compiled in.
//
// libopus keeps every DNN/model loader behind compile flags
// (ENABLE_DRED/ENABLE_OSCE/ENABLE_DEEP_PLC); a default build links none of
// them. The model loaders only make sense when a model-CONSUMING runtime is
// compiled in, so DNN-blob loading shares the same gate as the DRED/OSCE
// runtime hooks. In the default build it stays dormant and zero-cost.
const DNNBlob = false
