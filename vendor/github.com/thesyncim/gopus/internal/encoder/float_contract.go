package encoder

// round32 marks a float32 rounding boundary. Go permits a product to fuse
// with a later addition, including across statements, but an explicit
// float32 conversion prevents contraction across this boundary. Callers use
// it where the paired C reference rounds an intermediate product.
func round32(x float32) float32 { return float32(x) }

// fma32 evaluates a*b + c. The compiler can use a fused multiply-add on
// targets such as ARM64 and AMD64 v3; other targets use separate operations.
// Callers select the operands and intermediate rounding boundaries to match
// the paired C reference's contraction order.
func fma32(a, b, c float32) float32 { return a*b + c }
