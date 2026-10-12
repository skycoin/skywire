//go:build !amd64 || nosimd || purego

package celt

const libopusFloatInnerProdUsesSSEOrder = false
