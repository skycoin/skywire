// Package httputil pkg/httputil/dmsghttp.go
package httputil

// DMSGHTTPConf is struct of /dmsghttp endpoint of config bootstrap
type DMSGHTTPConf struct {
	DMSGServers        []DMSGServersConf `json:"dmsg_servers"`
	DMSGDiscovery      string            `json:"dmsg_discovery"`
	TranspordDiscovery string            `json:"transport_discovery"`
	AddressResolver    string            `json:"address_resolver"`
	RouteFinder        string            `json:"route_finder"`
	UptimeTracker      string            `json:"uptime_tracker"`
	ServiceDiscovery   string            `json:"service_discovery"`
}

// DMSGServersConf is struct of dmsg servers list on /dmsghttp endpoint
type DMSGServersConf struct {
	Static string `json:"static"`
	Server struct {
		Address string `json:"address"`
	} `json:"server"`
}
