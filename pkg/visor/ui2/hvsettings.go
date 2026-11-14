// Package ui2 pkg/visor/ui2/hvsettings.go c3-vis-core
package ui2

import (
	"slices"
	"strconv"
)

// RefreshCookie holds the visor list's poll interval in seconds. ui2.js
// keeps it in step with the Angular UI's refreshSeconds entry.
const RefreshCookie = "ui2-refresh"

// RefreshChoices are the Angular settings page's refresh rates
// (refresh-rate.component.ts).
var RefreshChoices = []int{3, 5, 10, 15, 30, 60, 90, 150, 300}

// PickRefresh is the cookie's interval if it is one of the choices, else 10.
func PickRefresh(cookie string) int {
	if n, err := strconv.Atoi(cookie); err == nil && slices.Contains(RefreshChoices, n) {
		return n
	}
	return 10
}

func pollEvery(s int) string {
	if s <= 0 {
		s = 10
	}
	return "every " + strconv.Itoa(s) + "s [document.visibilityState === 'visible']"
}
