//+build darwin

package vpn

const (
	GatewayForIfcCMDFmt = "netstat -rn | grep default | grep %s | awk '{print $2}'"
)
