//go:build !arm64 || purego

package celt

func kfBfly5N1Generic(fout []kissCpx, tw []kissCpx, m, fstride int) {
	kfBfly5Inner(fout, tw, m, 1, 1, fstride)
}
