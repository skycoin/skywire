package celt

// round32 is an explicit float32 conversion boundary. Go may fuse floating
// point operations, but it preserves the rounding required by this conversion
// instead of contracting across it. The matching libopus expression and build
// determine where callers place this boundary.
func round32(x float32) float32 {
	return float32(x)
}

// mulAdd32Ref rounds the product to float32 before adding c.
func mulAdd32Ref(a, b, c float32) float32 { return float32(a*b) + c }

// fma32 expresses a multiply-add whose result may use one rounding when the
// target supports contraction. mul32/add32/sub32 place float32 rounding
// boundaries between their operations.
func fma32(a, b, c float32) float32 { return a*b + c }

// mul32, add32 and sub32 spell the round32 boundary as a direct conversion,
// so each call inlines as a single level.
func mul32(a, b float32) float32 { return float32(a * b) }

func add32(a, b float32) float32 { return float32(a + b) }

func sub32(a, b float32) float32 { return float32(a - b) }
