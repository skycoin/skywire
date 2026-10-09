// Package multistream encodes and decodes Opus packets with multiple elementary
// streams, as used for surround sound and ambisonics.
//
// Encoder and Decoder map channels to mono or coupled stereo streams. The
// default constructors use Vorbis channel order for layouts with up to eight
// channels; explicit constructors accept a mapping table. Ambisonics helpers
// provide ACN/SN3D layouts, including projection encoding and decoding.
//
// PCM is interleaved by sample, and frame sizes are measured in samples per
// channel. Constructors copy mapping tables and projection matrices. Stateful
// Encoder and Decoder instances are not safe for concurrent use.
//
// Sample rates are 8, 12, 16, 24, or 48 kHz; 96 kHz and QEXT controls are
// available in builds tagged gopus_qext. DRED controls are available with
// gopus_dred or gopus_osce, while OSCE controls require gopus_osce.
package multistream
