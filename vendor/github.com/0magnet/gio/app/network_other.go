// SPDX-License-Identifier: Unlicense OR MIT

//go:build !android

package app

func startNetworkWatch() error { return nil }

func stopNetworkWatch() {}
