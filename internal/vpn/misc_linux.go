//+build linux

package vpn

const (
	GatewayForIfcCMDFmt = "route -n | grep %s | awk '$1 == \"0.0.0.0\" {print $2}'"
)
