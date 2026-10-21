// SPDX-License-Identifier: Unlicense OR MIT

//go:build !android

package app

func startForeground(string, string) error { return nil }

func stopForeground() {}
