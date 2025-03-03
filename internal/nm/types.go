// Package nm internal/nm/types.go
package nm

import "time"

// Status of network
type Status struct {
	LastUpdate   time.Time            `json:"last_update"`
	OnlineVisors int                  `json:"online_visors"`
	Transports   int                  `json:"alive_transports"`
	VPN          int                  `json:"available_vpn"`
	Skysocks     int                  `json:"available_skysocks"`
	PublicVisor  int                  `json:"available_public_visor"`
	LastCleaning *LastCleaningSummary `json:"last_cleaning"`
}

// LastCleaningSummary return a brief summary on last itterate of network monitor and cleaning dead entries
type LastCleaningSummary struct {
	AllDeadEntriesCleaned int `json:"all_dead_entries_cleaned"`
	Tpd                   int `json:"transport_discovery"`
	Ar                    struct {
		SUDPH int `json:"sudph"`
		STCPR int `json:"stcpr"`
	} `json:"address_resolver"`
	Dmsgd       int `json:"dmsg_discovery"`
	VPN         int `json:"vpn"`
	Skysocks    int `json:"skysocks"`
	PublicVisor int `json:"public_visor"`
}
