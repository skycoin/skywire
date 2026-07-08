//go:build !amd64 || purego

package celt

func celtInnerProdSSEStyleAsm(x, y []celtNorm) float32 {
	return celtInnerProdSSEStyleGo(x, y)
}
