package api

import (
	"reflect"
	"sync"
	"unsafe"

	kcp "github.com/0magnet/kcp-go/v5"
)

// listenerSessions reports how many sessions the KCP listener's own map holds
// and the addresses of those sessions, read under the listener's lock. The
// map is unexported, so this reaches it by reflection; it is diagnostic only.
func listenerSessions(l *kcp.Listener) (n int, ptrs map[uintptr]bool, ok bool) {
	if l == nil {
		return 0, nil, false
	}
	v := reflect.ValueOf(l).Elem()
	m, mu := v.FieldByName("sessions"), v.FieldByName("sessionLock")
	if !m.IsValid() || m.Kind() != reflect.Map || !mu.IsValid() || !mu.CanAddr() {
		return 0, nil, false
	}
	lock := (*sync.RWMutex)(unsafe.Pointer(mu.UnsafeAddr())) //nolint:gosec
	lock.RLock()
	defer lock.RUnlock()
	ptrs = make(map[uintptr]bool, m.Len())
	it := m.MapRange()
	for it.Next() {
		ptrs[it.Value().Pointer()] = true
	}
	return m.Len(), ptrs, true
}
