//go:build amd64 && !purego

package celt

//go:noescape
func celtInnerProdSSEStyleAsm(x, y []celtNorm) float32
