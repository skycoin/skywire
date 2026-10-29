// SPDX-License-Identifier: Unlicense OR MIT

package app

import "sync"

// NetworkInfo is what the system reports about the network the program's
// sockets use.
type NetworkInfo struct {
	// Moved is true when the sockets the program holds were left behind:
	// the system now routes through another network, or an address of ours
	// went away. A network seen for the first time is not a move.
	Moved bool
	// Metered is the system's metered flag. MeteredKnown is false when the
	// system did not say.
	Metered, MeteredKnown bool
	// Interface names the network's interface, if known.
	Interface string
}

var network struct {
	mu    sync.Mutex
	f     func(NetworkInfo)
	state netState
}

// WatchNetwork calls f when the network under the program changes or its
// metered flag flips, and once soon after with the current network. Calls
// come from a system thread, one at a time. A VPN is not a network of its
// own: the one under it is followed. On Android the app must import package
// gioui.org/app/permission/networkstate. Elsewhere it does nothing. Only one
// watch runs at a time, and a second call replaces f.
func WatchNetwork(f func(NetworkInfo)) (stop func(), err error) {
	network.mu.Lock()
	first := network.f == nil
	network.f = f
	network.state = netState{}
	network.mu.Unlock()
	if first {
		if err := startNetworkWatch(); err != nil {
			network.mu.Lock()
			network.f = nil
			network.mu.Unlock()
			return nil, err
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			network.mu.Lock()
			network.f = nil
			network.mu.Unlock()
			stopNetworkWatch()
		})
	}, nil
}

// networkChanged takes one system report. metered is -1 when unknown.
func networkChanged(id, ifname string, addrs []string, metered int, vpn bool) {
	network.mu.Lock()
	f := network.f
	info, news := network.state.observe(id, ifname, addrs, metered, vpn)
	network.mu.Unlock()
	if f != nil && news {
		f(info)
	}
}

func networkLost(id string) {
	network.mu.Lock()
	network.state.lost(id)
	network.mu.Unlock()
}

// netState decides which reports are news. Its zero value has seen nothing.
type netState struct {
	started bool // a network was seen at least once
	gone    bool // the network went away since
	id      string
	addrs   map[string]bool
	metered bool
	known   bool // metered has been reported
}

func (s *netState) observe(id, ifname string, addrs []string, metered int, vpn bool) (NetworkInfo, bool) {
	if vpn {
		return NetworkInfo{}, false
	}
	set := make(map[string]bool, len(addrs))
	for _, a := range addrs {
		if a != "" {
			set[a] = true
		}
	}
	first := !s.started
	moved := false
	if !first {
		moved = s.gone || s.id != id
		for a := range s.addrs {
			if !set[a] {
				moved = true
			}
		}
	}
	flip := metered >= 0 && (!s.known || s.metered != (metered == 1))
	s.started, s.gone, s.id, s.addrs = true, false, id, set
	if metered >= 0 {
		s.metered, s.known = metered == 1, true
	}
	if !first && !moved && !flip {
		return NetworkInfo{}, false
	}
	return NetworkInfo{Moved: moved, Metered: s.metered, MeteredKnown: s.known, Interface: ifname}, true
}

func (s *netState) lost(id string) {
	if s.started && s.id == id {
		s.gone = true
	}
}
